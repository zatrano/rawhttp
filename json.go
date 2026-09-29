package rawhttp

import "encoding/json"

// WriteJSON marshals v as JSON, sets Content-Type application/json, and SetBody.
func (c *Ctx) WriteJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.SetContentType("application/json")
	c.SetBody(b)
	return nil
}

// ReadJSON unmarshals the request body into v.
func (c *Ctx) ReadJSON(v any) error {
	return json.Unmarshal(c.Body(), v)
}

// SetJSON marshals v as the request body with Content-Type application/json.
func (r *Request) SetJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.SetBody(b)
	return r.SetHeader("Content-Type", "application/json")
}

// JSON unmarshals the response body into v.
func (r *Response) JSON(v any) error {
	return json.Unmarshal(r.Body(), v)
}
