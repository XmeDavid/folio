package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteServiceError(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		rr := httptest.NewRecorder()
		WriteServiceError(rr, NewValidationError("bad input"))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", rr.Code)
		}
	})
	t.Run("not found", func(t *testing.T) {
		rr := httptest.NewRecorder()
		WriteServiceError(rr, NewNotFoundError("thing"))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d", rr.Code)
		}
	})
}
