package imperative

import (
	"reflect"
	"testing"
)

func TestImperative_Multiply(t *testing.T) {
	imp := Imperative{Response: Response{StatusCode: 200}}
	got := imp.Multiply(2)
	if len(got) != 2 {
		t.Errorf("Multiply() = %d, want 2", len(got))
	}
	for _, imp := range got {
		if imp.Response.StatusCode != 200 {
			t.Errorf("Multiply() = %d, want 200", imp.Response.StatusCode)
		}
	}
}

func TestImperative_Seed(t *testing.T) {
	imp := Imperative{
		Request:  Request{Path: "/test", Method: "GET", Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 1_000_000_000 + 42}},
		Response: Response{StatusCode: 200},
	}
	imps := imp.Multiply(42)
	got := imps.Seed(1337)
	for _, imp := range got {
		if imp.Request.Body.Seed <= 0 {
			t.Errorf("Seed() = %d, want non-zero", imp.Response.Body.Seed)
		}
		if imp.Request.Body.Seed >= 1_000_000_000 {
			t.Errorf("Seed() = %d, generated seed should be less or equal to 1_000_000_000", imp.Response.Body.Seed)
		}
		// response body has no length, so no seed should be set
		if imp.Response.Body.Seed != 0 {
			t.Errorf("Seed() = %d, want 0", imp.Response.Body.Seed)
		}
	}
}

func TestImperative_Hash(t *testing.T) {
	imp1 := Imperative{Response: Response{StatusCode: 200}, Request: Request{Path: "/test", Method: "GET", Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}}
	hash1 := imp1.Hash()
	if hash1 == "" {
		t.Errorf("Hash() = %s, want non-empty string", hash1)
	}
	// re-encode and hash again
	imp2, err := ParseImperative(imp1.Encode())
	if err != nil {
		t.Errorf("ParseImperative() = %v, want nil", err)
	}
	hash2 := imp2.Hash()
	if hash1 != hash2 {
		t.Errorf("Hash() = %s, want %s", hash2, hash1)
	}
}

func TestImperatives_Hash(t *testing.T) {
	imp := Imperative{Response: Response{StatusCode: 200}, Request: Request{Path: "/test", Method: "GET", Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}}
	imps := imp.Multiply(42).Seed(1337)
	hashMap := imps.Hash()
	if len(hashMap) != 42 {
		t.Errorf("Hash() = %d, want 42", len(hashMap))
	}
	for _, imp := range imps {
		got := hashMap[imp.Hash()]
		for _, gotImp := range got {
			if !reflect.DeepEqual(imp, gotImp) {
				t.Errorf("Hash() = %s, want %s", imp.Encode(), gotImp.Encode())
			}
		}
	}

}

func TestImperatives_Multiply(t *testing.T) {
	imps := Imperatives{
		{Response: Response{StatusCode: 200}},
		{Response: Response{StatusCode: 404}},
		{Response: Response{StatusCode: 500}},
	}
	got := imps.Multiply(2)
	if len(got) != 6 {
		t.Errorf("Multiply() = %d, want 4", len(got))
	}
	counts := make(map[int]int)
	for _, imp := range got {
		counts[imp.Response.StatusCode]++
	}

	if counts[200] != 2 || counts[404] != 2 || counts[500] != 2 {
		t.Errorf("Multiply() = %v, want 2 for each status code", counts)
	}
}

func TestImperative_Validate(t *testing.T) {
	imp := Imperative{Response: Response{StatusCode: 200, Body: Body{Length: 1024, Filepath: "test.txt", Seed: 42}}}

	if err := imp.Validate(); err == nil {
		t.Errorf("filepath and length set at the same time should error")
	}
	imp = Imperative{Response: Response{StatusCode: 200, Body: Body{Length: 1024, Encoding: "utf-8", Seed: 42}}}
	if err := imp.Validate(); err == nil {
		t.Errorf("encoding set to unknown value should error")
	}
	imp = Imperative{Request: Request{Body: Body{Length: 1024, Encoding: EncodingUTF8, Seed: 42}}, Response: Response{StatusCode: 200}}
	if err := imp.Validate(); err != nil {
		t.Errorf("request encoding set to valid value should not error")
	}
	imp = Imperative{Request: Request{Body: Body{Length: 1024, Encoding: EncodingBinary}}, Response: Response{StatusCode: 200}}
	if err := imp.Validate(); err == nil {
		t.Errorf("no seed set should error")
	}
}
