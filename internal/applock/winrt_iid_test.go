package applock

import "testing"

// The expected values are the IIDs Windows SDK headers publish for these
// instances (windows.foundation.h), so they check the hash, the byte order
// and the version/variant bits all at once.
func TestAsyncOperationIID(t *testing.T) {
	for _, tc := range []struct {
		signature string
		want      string
	}{
		{"b1", "{cdb5efb3-5788-509d-9be1-71ccb8a3362a}"},
		{"string", "{3e1fe603-f897-5263-b328-0806426b8a79}"},
	} {
		if got := asyncOperationIID(tc.signature).String(); got != tc.want {
			t.Errorf("IAsyncOperation<%s>: got %s, want %s", tc.signature, got, tc.want)
		}
	}
}

func TestGUIDString(t *testing.T) {
	const want = "{9fc2b0bb-e446-44e2-aa61-9cab8f636af2}"
	if got := iidIAsyncOperation.String(); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
