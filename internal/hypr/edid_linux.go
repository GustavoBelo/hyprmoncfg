package hypr

import (
	"os"
	"path/filepath"
	"strings"
)

// EDID CTA-861 data block tags. The HDR Static Metadata block lives in the
// "use extended tag" family, and is what tells us a display can actually do
// HDR -- Hyprland's `cm` setting reports what is configured, never what the
// panel supports.
const (
	ctaExtensionTag      = 0x02
	ctaUseExtendedTag    = 0x07
	ctaHDRStaticMetadata = 0x06

	// Electro-optical transfer functions, as bits of the first payload byte.
	// Bit 0 is plain SDR and says nothing; PQ and HLG are the HDR ones.
	eotfSMPTEST2084 = 1 << 2
	eotfHLG         = 1 << 3
)

// HDRCapableConnectors reports which connectors advertise an HDR transfer
// function in their EDID, keyed by connector name ("HDMI-A-1").
//
// A missing or unreadable EDID yields no entry rather than a guess: offering
// HDR on a panel that cannot do it is a dead toggle, and claiming a panel
// cannot when it can is worse.
func HDRCapableConnectors() map[string]bool {
	return hdrCapableConnectors(defaultDRMSysRoot)
}

func hdrCapableConnectors(sysRoot string) map[string]bool {
	matches, err := filepath.Glob(filepath.Join(sysRoot, "card*-*"))
	if err != nil {
		return nil
	}
	capable := make(map[string]bool, len(matches))
	for _, path := range matches {
		_, name, ok := strings.Cut(filepath.Base(path), "-")
		if !ok || name == "" {
			continue
		}
		edid, err := os.ReadFile(filepath.Join(path, "edid"))
		if err != nil || len(edid) == 0 {
			continue
		}
		capable[name] = edidAdvertisesHDR(edid)
	}
	return capable
}

// edidAdvertisesHDR walks the CTA-861 extension blocks looking for an HDR
// Static Metadata block that names PQ or HLG.
func edidAdvertisesHDR(edid []byte) bool {
	const blockSize = 128
	for offset := blockSize; offset+blockSize <= len(edid); offset += blockSize {
		block := edid[offset : offset+blockSize]
		if block[0] != ctaExtensionTag {
			continue
		}
		// Byte 2 is where the detailed timing descriptors start, which is also
		// where the data block collection ends.
		end := int(block[2])
		if end > blockSize {
			end = blockSize
		}
		for i := 4; i < end; {
			tag := block[i] >> 5
			length := int(block[i] & 0x1F)
			if length == 0 || i+1+length > blockSize {
				break
			}
			if tag == ctaUseExtendedTag && length >= 2 && block[i+1] == ctaHDRStaticMetadata {
				if block[i+2]&(eotfSMPTEST2084|eotfHLG) != 0 {
					return true
				}
			}
			i += 1 + length
		}
	}
	return false
}

// PreferredModes reports each connector's first DRM mode, keyed by connector
// name. The kernel lists the EDID-preferred mode first, which is the display's
// native resolution and therefore its real aspect ratio.
//
// This matters because the largest mode a TV reports is not always the one it
// is built around: the Samsung on the development host offers 4096x2160, a
// 17:9 cinema mode, above its native 16:9 3840x2160.
func PreferredModes() map[string]string {
	return preferredModes(defaultDRMSysRoot)
}

func preferredModes(sysRoot string) map[string]string {
	matches, err := filepath.Glob(filepath.Join(sysRoot, "card*-*"))
	if err != nil {
		return nil
	}
	preferred := make(map[string]string, len(matches))
	for _, path := range matches {
		_, name, ok := strings.Cut(filepath.Base(path), "-")
		if !ok || name == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(path, "modes"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				preferred[name] = line
				break
			}
		}
	}
	return preferred
}
