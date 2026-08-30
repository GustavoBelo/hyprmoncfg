package hypr

import (
	"os"
	"path/filepath"
	"testing"
)

// buildEDID assembles a base block plus one CTA-861 extension carrying the
// given data blocks.
func buildEDID(dataBlocks []byte) []byte {
	edid := make([]byte, 256)
	ext := edid[128:]
	ext[0] = ctaExtensionTag
	ext[1] = 3
	ext[2] = byte(4 + len(dataBlocks)) // where the DTDs begin
	copy(ext[4:], dataBlocks)
	return edid
}

func hdrBlock(eotf byte) []byte {
	// tag 0x07 (extended), length 3: extended tag, eotf bitmap, metadata types.
	return []byte{(ctaUseExtendedTag << 5) | 3, ctaHDRStaticMetadata, eotf, 0x00}
}

func TestEDIDAdvertisesHDR(t *testing.T) {
	cases := []struct {
		name string
		edid []byte
		want bool
	}{
		// The Samsung TV on this host reports 0x0d: SDR + PQ + HLG.
		{"pq and hlg", buildEDID(hdrBlock(0x0d)), true},
		{"pq only", buildEDID(hdrBlock(eotfSMPTEST2084)), true},
		{"hlg only", buildEDID(hdrBlock(eotfHLG)), true},
		{"traditional sdr only", buildEDID(hdrBlock(0x01)), false},
		{"no hdr block", buildEDID([]byte{(0x01 << 5) | 3, 0x00, 0x00, 0x00}), false},
		{"no extension", make([]byte, 128), false},
		{"empty", nil, false},
		{"truncated", []byte{0x00, 0xff}, false},
	}
	for _, tc := range cases {
		if got := edidAdvertisesHDR(tc.edid); got != tc.want {
			t.Fatalf("%s: edidAdvertisesHDR = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A zero-length data block advances the cursor by one byte at minimum;
// without that guard the walk spins on it forever.
func TestEDIDAdvertisesHDRTerminatesOnPadding(t *testing.T) {
	if edidAdvertisesHDR(buildEDID([]byte{0x00, 0x00, 0x00, 0x00})) {
		t.Fatal("padding must not read as HDR")
	}
}

func TestHDRCapableConnectorsReadsSysfs(t *testing.T) {
	root := t.TempDir()
	write := func(connector string, edid []byte) {
		t.Helper()
		dir := filepath.Join(root, connector)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "edid"), edid, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("card1-HDMI-A-1", buildEDID(hdrBlock(0x0d)))
	write("card1-DP-2", buildEDID(hdrBlock(0x01)))

	capable := hdrCapableConnectors(root)
	if !capable["HDMI-A-1"] {
		t.Fatal("the HDR TV should be reported as capable")
	}
	if capable["DP-2"] {
		t.Fatal("an SDR-only panel must not be reported as capable")
	}
}
