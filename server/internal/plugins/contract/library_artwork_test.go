package contract

import "testing"

func TestNormalizeLibraryArtworkCandidatesRequiresStableIDsAndOpaqueAssets(t *testing.T) {
	valid := []byte(`[{"id":"BV1example","assetRef":"11111111-1111-4111-8111-111111111111"}]`)
	items, err := NormalizeLibraryArtworkCandidates(valid)
	if err != nil || len(items) != 1 || items[0].ID != "BV1example" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for _, invalid := range [][]byte{
		[]byte(`[{"id":"BV1example","assetRef":"https://example.test/poster.jpg"}]`),
		[]byte(`[{"id":"same","assetRef":"11111111-1111-4111-8111-111111111111"},{"id":"same","assetRef":"22222222-2222-4222-8222-222222222222"}]`),
		[]byte(`[{"id":"bad\nidentity","assetRef":"11111111-1111-4111-8111-111111111111"}]`),
	} {
		if _, err := NormalizeLibraryArtworkCandidates(invalid); err == nil {
			t.Fatalf("invalid candidates accepted: %s", invalid)
		}
	}
}
