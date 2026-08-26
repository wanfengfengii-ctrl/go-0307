package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"lyophilizer-sterilization-validation/internal/domain"
)

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// writeError maps a domain error to a stable HTTP response. Unknown errors are
// wrapped as a 500 without leaking internal detail.
func writeError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if errors.As(err, &de) {
		status := http.StatusConflict
		if de.Code == domain.CodeNotFound {
			status = http.StatusNotFound
		}
		writeJSON(w, status, de)
		return
	}
	writeJSON(w, http.StatusInternalServerError, domain.NewError("INTERNAL", "", false, "internal error"))
}

// decode strictly parses a JSON request body into dst.
func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
