package pgarray

import (
	"reflect"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	in := StringArray{"Go", `Next.js "app"`, `back\slash`, "a,b", ""}
	v, err := in.Value()
	if err != nil {
		t.Fatal(err)
	}
	var out StringArray
	if err := out.Scan(v); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip: got %q want %q", out, in)
	}
}

func TestEmptyAndNil(t *testing.T) {
	var a StringArray
	if v, _ := a.Value(); v != "{}" {
		t.Errorf("empty value = %v", v)
	}
	if err := a.Scan(nil); err != nil || len(a) != 0 {
		t.Errorf("nil scan: %v %v", a, err)
	}
	if err := a.Scan([]byte("{}")); err != nil || len(a) != 0 {
		t.Errorf("empty scan: %v %v", a, err)
	}
	if err := a.Scan(42); err == nil {
		t.Error("unsupported type must fail")
	}
}
