package classification

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// ---- categories ------------------------------------------------------------

func TestCategoryCreateInput_normalize(t *testing.T) {
	t.Run("trims and accepts", func(t *testing.T) {
		in := CategoryCreateInput{Name: "  Groceries  "}
		out, err := in.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if out.Name != "Groceries" {
			t.Errorf("name = %q, want Groceries", out.Name)
		}
	})
	t.Run("empty name rejected", func(t *testing.T) {
		_, err := CategoryCreateInput{Name: "   "}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
		var verr *httpx.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("want ValidationError, got %T", err)
		}
	})
}

func TestCategoryPatchInput_normalize(t *testing.T) {
	t.Run("empty name rejected", func(t *testing.T) {
		blank := "   "
		_, err := CategoryPatchInput{Name: &blank}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("clear parent via empty string", func(t *testing.T) {
		empty := ""
		out, err := CategoryPatchInput{ParentID: &empty}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.parentIDSet || !out.parentIDNull {
			t.Error("empty parentId should clear")
		}
	})
	t.Run("bad parent uuid", func(t *testing.T) {
		bad := "not-a-uuid"
		_, err := CategoryPatchInput{ParentID: &bad}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("valid parent uuid", func(t *testing.T) {
		id := uuid.New().String()
		out, err := CategoryPatchInput{ParentID: &id}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.parentIDSet || out.parentIDNull {
			t.Error("parentId should be set, not null")
		}
	})
	t.Run("color empty clears", func(t *testing.T) {
		empty := ""
		out, err := CategoryPatchInput{Color: &empty}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.colorSet || !out.colorNull {
			t.Error("empty color should clear")
		}
	})
	t.Run("archived toggle", func(t *testing.T) {
		tr := true
		out, err := CategoryPatchInput{Archived: &tr}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.archivedSet || !out.archived {
			t.Error("archived should be set to true")
		}
	})
}

// ---- merchants -------------------------------------------------------------

func TestMerchantCreateInput_normalize(t *testing.T) {
	t.Run("trims and accepts", func(t *testing.T) {
		in := MerchantCreateInput{CanonicalName: "  Migros  "}
		out, err := in.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if out.CanonicalName != "Migros" {
			t.Errorf("canonicalName = %q, want Migros", out.CanonicalName)
		}
	})
	t.Run("empty canonical name rejected", func(t *testing.T) {
		_, err := MerchantCreateInput{CanonicalName: "   "}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestMerchantPatchInput_normalize(t *testing.T) {
	t.Run("empty name rejected", func(t *testing.T) {
		blank := "   "
		_, err := MerchantPatchInput{CanonicalName: &blank}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("clear defaultCategoryId via empty", func(t *testing.T) {
		empty := ""
		out, err := MerchantPatchInput{DefaultCategoryID: &empty}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.defaultCategoryIDSet || !out.defaultCategoryIDNull {
			t.Error("empty defaultCategoryId should clear")
		}
	})
	t.Run("bad defaultCategoryId uuid", func(t *testing.T) {
		bad := "not-a-uuid"
		_, err := MerchantPatchInput{DefaultCategoryID: &bad}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("clear nullable strings", func(t *testing.T) {
		empty := ""
		out, err := MerchantPatchInput{
			LogoURL:  &empty,
			Industry: &empty,
			Website:  &empty,
			Notes:    &empty,
		}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !(out.logoURLSet && out.logoURLNull) ||
			!(out.industrySet && out.industryNull) ||
			!(out.websiteSet && out.websiteNull) ||
			!(out.notesSet && out.notesNull) {
			t.Error("all nullable strings should be cleared")
		}
	})
}

// ---- tags ------------------------------------------------------------------

func TestTagCreateInput_normalize(t *testing.T) {
	t.Run("trims and accepts", func(t *testing.T) {
		in := TagCreateInput{Name: "  reimbursable  "}
		out, err := in.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if out.Name != "reimbursable" {
			t.Errorf("name = %q, want reimbursable", out.Name)
		}
	})
	t.Run("empty rejected", func(t *testing.T) {
		_, err := TagCreateInput{Name: "   "}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestTagPatchInput_normalize(t *testing.T) {
	t.Run("empty name rejected", func(t *testing.T) {
		blank := "   "
		_, err := TagPatchInput{Name: &blank}.normalize()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("color clear", func(t *testing.T) {
		empty := ""
		out, err := TagPatchInput{Color: &empty}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.colorSet || !out.colorNull {
			t.Error("empty color should clear")
		}
	})
	t.Run("archived toggle", func(t *testing.T) {
		f := false
		out, err := TagPatchInput{Archived: &f}.normalize()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !out.archivedSet || out.archived {
			t.Error("archived should be set to false")
		}
	})
}
