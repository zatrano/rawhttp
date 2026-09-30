//go:build rawhttp_poison

package rawhttp

// PoisonBuildEnabled reports whether this binary was built with -tags rawhttp_poison.
func PoisonBuildEnabled() bool { return true }

func poisonBytes(b []byte) {
	for i := range b {
		b[i] = 0xDE
	}
}

// poisonPinnedBuffer fills the live request pin window with 0xDE so slices that
// still alias cr.buf fail loudly after Handler. Must run before cr.release().
// Does not touch owned copies (reqBody / *Copy) so writeResponse can still echo
// SetBody(ctx.Body()) references.
func poisonPinnedBuffer(cr *connReader) {
	if cr == nil || cr.buf == nil || cr.off <= cr.r {
		return
	}
	poisonBytes(cr.buf[cr.r:cr.off])
}

// poisonCtxRequestSlices fills owned request copies and clears Ctx request
// slice fields. Call after writeResponse so response body refs stay intact.
func poisonCtxRequestSlices(ctx *Ctx) {
	poisonBytes(ctx.upgradeProto)
	ctx.upgradeProto = nil
	ctx.upgradeWanted = false
	poisonBytes(ctx.Method)
	poisonBytes(ctx.Path)
	poisonBytes(ctx.Query)
	poisonBytes(ctx.host)
	poisonBytes(ctx.headerBlock)
	poisonBytes(ctx.reqContentType)
	poisonBytes(ctx.userAgent)
	poisonBytes(ctx.accept)
	poisonBytes(ctx.acceptEncoding)
	poisonBytes(ctx.cookieHdr)
	poisonBytes(ctx.referer)
	poisonBytes(ctx.authorization)
	poisonBytes(ctx.origin)
	for i := range ctx.extraKeys {
		poisonBytes(ctx.extraKeys[i])
	}
	for i := range ctx.extraVals {
		poisonBytes(ctx.extraVals[i])
	}
	poisonBytes(ctx.reqBody)
	poisonBytes(ctx.uriBuf)
	poisonBytes(ctx.methodCopy)
	poisonBytes(ctx.pathCopy)
	poisonBytes(ctx.queryCopy)
	poisonBytes(ctx.hostCopy)
	poisonBytes(ctx.hdrCopy)
	poisonBytes(ctx.ctCopy)
	poisonBytes(ctx.uaCopy)
	poisonBytes(ctx.acceptCopy)
	poisonBytes(ctx.acceptEncCopy)
	poisonBytes(ctx.cookieCopy)
	poisonBytes(ctx.refererCopy)
	poisonBytes(ctx.authCopy)
	poisonBytes(ctx.originCopy)
	if ctx.uri != nil {
		poisonBytes(ctx.uri.scheme)
		poisonBytes(ctx.uri.host)
		poisonBytes(ctx.uri.path)
		poisonBytes(ctx.uri.query)
		poisonBytes(ctx.uri.hash)
		poisonBytes(ctx.uri.username)
		poisonBytes(ctx.uri.password)
		ctx.uri.Reset()
	}

	ctx.Method = nil
	ctx.Path = nil
	ctx.Query = nil
	ctx.host = nil
	ctx.headerBlock = nil
	ctx.upgradeProto = nil
	ctx.upgradeWanted = false
	ctx.reqContentType = nil
	ctx.userAgent = nil
	ctx.accept = nil
	ctx.acceptEncoding = nil
	ctx.cookieHdr = nil
	ctx.referer = nil
	ctx.authorization = nil
	ctx.origin = nil
	ctx.extraKeys = nil
	ctx.extraVals = nil
	ctx.reqBody = nil
	ctx.uriBuf = nil
}
