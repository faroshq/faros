package service

import (
	"encoding/json"
	"io"
	"net/http"
)

const (
	BYTE = 1 << (10 * iota)
	KILOBYTE
	MEGABYTE
)

func read(r *http.Request, req interface{}) error {
	// Protecting the API from too big uploads
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*MEGABYTE))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, &req)
}
