package rawhttp

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"sync"
)

const (
	defaultMaxMultipartMemory = 16 << 20 // 16 MiB
	defaultMaxMultipartFiles  = 32
	defaultMaxMultipartParts  = 128
	maxMultipartBoundaryLen   = 70 // RFC 2046
)

// MultipartForm holds parsed multipart/form-data fields and files.
type MultipartForm struct {
	Value map[string][]string
	File  map[string][]*multipart.FileHeader
}

var multipartFormPool = sync.Pool{New: func() any { return new(MultipartForm) }}

// MultipartForm parses multipart/form-data up to maxMemory bytes kept in memory
// (remainder spills to temp files per mime/multipart). Zero → 16MiB.
// Results are cached on the Ctx for the request lifetime.
func (c *Ctx) MultipartForm(maxMemory int64) (*MultipartForm, error) {
	if c.multipart != nil {
		return c.multipart, nil
	}
	ct := string(c.reqContentType)
	mediatype, params, err := mime.ParseMediaType(ct)
	if err != nil || mediatype != "multipart/form-data" {
		return nil, ErrBadRequest
	}
	boundary := params["boundary"]
	if boundary == "" || len(boundary) > maxMultipartBoundaryLen {
		return nil, ErrBadRequest
	}
	if maxMemory <= 0 {
		if c.maxMultipartMemory > 0 {
			maxMemory = c.maxMultipartMemory
		} else {
			maxMemory = defaultMaxMultipartMemory
		}
	}
	r := multipart.NewReader(bytes.NewReader(c.reqBody), boundary)
	form, err := r.ReadForm(maxMemory)
	if err != nil {
		return nil, err
	}
	if err := c.validateMultipartLimits(form); err != nil {
		_ = form.RemoveAll()
		return nil, err
	}
	mf := multipartFormPool.Get().(*MultipartForm)
	mf.Value = form.Value
	mf.File = form.File
	c.multipart = mf
	c.multipartNative = form
	return mf, nil
}

func (c *Ctx) validateMultipartLimits(form *multipart.Form) error {
	maxParts := defaultMaxMultipartParts
	maxFiles := defaultMaxMultipartFiles
	if c != nil {
		if c.maxMultipartParts > 0 {
			maxParts = c.maxMultipartParts
		}
		if c.maxMultipartFiles > 0 {
			maxFiles = c.maxMultipartFiles
		}
	}
	nParts := 0
	nFiles := 0
	for _, vals := range form.Value {
		nParts += len(vals)
	}
	for _, files := range form.File {
		nFiles += len(files)
		nParts += len(files)
	}
	if nParts > maxParts {
		return ErrBadRequest
	}
	if nFiles > maxFiles {
		return ErrBadRequest
	}
	return nil
}

// FormFile returns the first file for the given form field name.
func (c *Ctx) FormFile(name string) (*multipart.FileHeader, error) {
	mf, err := c.MultipartForm(0)
	if err != nil {
		return nil, err
	}
	files := mf.File[name]
	if len(files) == 0 {
		return nil, ErrBadRequest
	}
	return files[0], nil
}

// MultipartValue returns the first text field value from a multipart form.
func (c *Ctx) MultipartValue(name string) string {
	mf, err := c.MultipartForm(0)
	if err != nil || mf == nil {
		return ""
	}
	vals := mf.Value[name]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func releaseMultipart(c *Ctx) {
	if c.multipartNative != nil {
		_ = c.multipartNative.RemoveAll()
		c.multipartNative = nil
	}
	if c.multipart != nil {
		c.multipart.Value = nil
		c.multipart.File = nil
		multipartFormPool.Put(c.multipart)
		c.multipart = nil
	}
}

// ensure FormValue also checks multipart text fields after urlencoded.
func formValueMultipart(c *Ctx, key string) []byte {
	if !isMultipartForm(c.reqContentType) {
		return nil
	}
	mf, err := c.MultipartForm(0)
	if err != nil || mf == nil {
		return nil
	}
	vals := mf.Value[key]
	if len(vals) == 0 {
		return nil
	}
	return []byte(vals[0])
}

func isMultipartForm(ct []byte) bool {
	const want = "multipart/form-data"
	if len(ct) < len(want) {
		return false
	}
	if !equalFoldStr(ct[:len(want)], want) {
		return false
	}
	if len(ct) == len(want) {
		return true
	}
	return ct[len(want)] == ';' || ct[len(want)] == ' '
}

// Open opens the multipart file for reading.
func OpenMultipartFile(fh *multipart.FileHeader) (multipart.File, error) {
	if fh == nil {
		return nil, ErrBadRequest
	}
	return fh.Open()
}

// ReadMultipartFile reads the entire multipart file into memory (capped by maxSize).
func ReadMultipartFile(fh *multipart.FileHeader, maxSize int64) ([]byte, error) {
	if fh == nil {
		return nil, ErrBadRequest
	}
	if maxSize <= 0 {
		maxSize = defaultMaxMultipartMemory
	}
	if fh.Size > maxSize {
		return nil, ErrBodyTooLarge
	}
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, ErrBodyTooLarge
	}
	return data, nil
}

// SaveMultipartFile copies a multipart file to dstPath (overwrites if present).
// Size is capped by maxSize (zero → 16MiB).
func SaveMultipartFile(fh *multipart.FileHeader, dstPath string, maxSize int64) error {
	if fh == nil || dstPath == "" {
		return ErrBadRequest
	}
	if maxSize <= 0 {
		maxSize = defaultMaxMultipartMemory
	}
	if fh.Size > maxSize {
		return ErrBodyTooLarge
	}
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()

	n, err := io.Copy(dst, io.LimitReader(src, maxSize+1))
	if err != nil {
		_ = os.Remove(dstPath)
		return err
	}
	if n > maxSize {
		_ = os.Remove(dstPath)
		return ErrBodyTooLarge
	}
	return nil
}
