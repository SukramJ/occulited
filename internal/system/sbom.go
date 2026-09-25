package system

import (
	"os"
)

// SBOMFile is the image's software bill of materials (task 179): CycloneDX 1.6 JSON, gzip, with
// the licence texts, written by the fork's post-build from buildroot's show-info and legal-info,
// the HmIP server's JAR libraries, occulited's Go modules and its UI bundle. World-readable; the
// same file is the release asset.
const SBOMFile = "/usr/share/openccu-lite/sbom.cdx.json.gz"

// ReadSBOM returns the gzip file as it is on disk.
func (r Root) ReadSBOM() ([]byte, error) {
	return os.ReadFile(r.join(SBOMFile))
}
