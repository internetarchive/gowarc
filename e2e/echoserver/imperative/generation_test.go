package imperative

import (
	"bytes"
	"testing"
)

func TestBody_Validate(t *testing.T) {
	imp := Imperative{Response: Response{StatusCode: 200, Body: Body{Length: 1024, Filepath: "test.txt", Seed: 42}}}
	if err := imp.Response.Body.Validate(); err == nil {
		t.Errorf("filepath and length set at the same time should error")
	}
	imp = Imperative{Response: Response{StatusCode: 200, Body: Body{Length: 1024, Encoding: "utf-8", Seed: 42}}}
	if err := imp.Response.Body.Validate(); err == nil {
		t.Errorf("encoding set to unknown value should error")
	}
	imp = Imperative{Response: Response{StatusCode: 200, Body: Body{Length: 1024, Encoding: EncodingUTF8}}}
	if err := imp.Response.Body.Validate(); err == nil {
		t.Errorf("unset seed should error")
	}
}
func TestBody_Build(t *testing.T) {
	// utf-8 body
	imp1 := Imperative{Response: Response{StatusCode: 200, Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}}
	imp2 := Imperative{Response: Response{StatusCode: 404, Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}}
	body1, err1 := imp1.Response.Body.Build()
	if err1 != nil {
		t.Errorf("Build() utf-8 = %v, want nil", err1)
	}
	body2, err2 := imp2.Response.Body.Build()
	if err2 != nil {
		t.Errorf("Build() utf-8 = %v, want nil", err2)
	}
	if !bytes.Equal(body1, body2) {
		t.Errorf("Build() utf-8 = %v, want %v", string(body1), string(body2))
	}

	// binary body
	imp3 := Imperative{Response: Response{StatusCode: 200, Body: Body{Length: 1024, Encoding: EncodingBinary, Seed: 42}}}
	imp4 := Imperative{Response: Response{StatusCode: 404, Body: Body{Length: 1024, Encoding: EncodingBinary, Seed: 42}}}
	body3, err3 := imp3.Response.Body.Build()
	if err3 != nil {
		t.Errorf("Build() binary = %v, want nil", err3)
	}
	body4, err4 := imp4.Response.Body.Build()
	if err4 != nil {
		t.Errorf("Build() binary = %v, want nil", err4)
	}
	if !bytes.Equal(body3, body4) {
		t.Errorf("Build() binary = %v, want %v", string(body3), string(body4))
	}

	// empty body should return empty []byte
	imp5 := Imperative{Response: Response{StatusCode: 200, Body: Body{}}}
	body5, err5 := imp5.Response.Body.Build()
	if err5 != nil {
		t.Errorf("Build() empty = %v, want nil", err5)
	}
	if len(body5) != 0 {
		t.Errorf("Build() empty = %v, want empty", string(body5))
	}
}

func TestBody_Build_Compress(t *testing.T) {
	// utf-8 body
	imp1 := Imperative{Response: Response{StatusCode: 200, Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42, Compress: "gzip"}}}
	imp2 := Imperative{Response: Response{StatusCode: 404, Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42, Compress: "gzip"}}}
	body1, err1 := imp1.Response.Body.Build()
	if err1 != nil {
		t.Errorf("Build() utf-8 = %v, want nil", err1)
	}
	body2, err2 := imp2.Response.Body.Build()
	if err2 != nil {
		t.Errorf("Build() utf-8 = %v, want nil", err2)
	}
	if !bytes.Equal(body1, body2) {
		t.Errorf("Build() utf-8 = %v, want %v", string(body1), string(body2))
	}

	// binary body
	imp3 := Imperative{Response: Response{StatusCode: 200, Body: Body{Length: 1024, Encoding: EncodingBinary, Seed: 42, Compress: "zstd"}}}
	imp4 := Imperative{Response: Response{StatusCode: 404, Body: Body{Length: 1024, Encoding: EncodingBinary, Seed: 42, Compress: "zstd"}}}
	body3, err3 := imp3.Response.Body.Build()
	if err3 != nil {
		t.Errorf("Build() binary = %v, want nil", err3)
	}
	body4, err4 := imp4.Response.Body.Build()
	if err4 != nil {
		t.Errorf("Build() binary = %v, want nil", err4)
	}
	if !bytes.Equal(body3, body4) {
		t.Errorf("Build() binary = %v, want %v", string(body3), string(body4))
	}

	// empty body should return empty []byte
	imp5 := Imperative{Response: Response{StatusCode: 200, Body: Body{Compress: "gzip"}}}
	body5, err5 := imp5.Response.Body.Build()
	if err5 != nil {
		t.Errorf("Build() empty = %v, want nil", err5)
	}
	if len(body5) != 0 {
		t.Errorf("Build() empty = %v, want empty", string(body5))
	}
}
