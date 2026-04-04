package metadatautil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type reproducibleBuildPlatformDigest struct {
	ImageManifestDigest string `json:"image_manifest_digest"`
	ConfigDigest        string `json:"config_digest"`
}

type reproducibleBuildManifest struct {
	OCISubjectDigest string                                     `json:"oci_subject_digest"`
	SourceDateEpoch  int64                                      `json:"source_date_epoch"`
	Platforms        map[string]reproducibleBuildPlatformDigest `json:"platforms"`
}

type reproducibleBuildReport struct {
	subjectDigest   string
	manifestDigests map[string]string
	configDigests   map[string]string
}

func VerifyReproducibleBuild(layoutA, layoutB, platformsCSV, manifestPath string, sourceDateEpoch int64) error {
	platforms, err := parseReproducibleBuildPlatforms(platformsCSV)
	if err != nil {
		return err
	}
	reportA, err := inspectOCIExport(layoutA, platforms)
	if err != nil {
		return err
	}
	reportB, err := inspectOCIExport(layoutB, platforms)
	if err != nil {
		return err
	}

	if err := compareReproducibleBuildReports(reportA, reportB, platforms); err != nil {
		return err
	}

	if manifestPath == "" {
		return nil
	}
	return writeJSONFile(manifestPath, reproducibleBuildManifestFromReport(reportA, platforms, sourceDateEpoch))
}

func GenerateReproducibleBuildManifest(layout, platformsCSV, manifestPath string, sourceDateEpoch int64) error {
	platforms, err := parseReproducibleBuildPlatforms(platformsCSV)
	if err != nil {
		return err
	}
	report, err := inspectOCIExport(layout, platforms)
	if err != nil {
		return err
	}
	return writeJSONFile(manifestPath, reproducibleBuildManifestFromReport(report, platforms, sourceDateEpoch))
}

func VerifyReproducibleBuildManifest(layout, platformsCSV, manifestPath string) error {
	platforms, err := parseReproducibleBuildPlatforms(platformsCSV)
	if err != nil {
		return err
	}
	report, err := inspectOCIExport(layout, platforms)
	if err != nil {
		return err
	}
	var manifest reproducibleBuildManifest
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return err
	}
	return compareReproducibleBuildReportToManifest(report, manifest, platforms)
}

func reproducibleBuildManifestFromReport(report reproducibleBuildReport, platforms []string, sourceDateEpoch int64) reproducibleBuildManifest {
	platformManifests := make(map[string]reproducibleBuildPlatformDigest, len(platforms))
	for _, platform := range platforms {
		platformManifests[platform] = reproducibleBuildPlatformDigest{
			ImageManifestDigest: report.manifestDigests[platform],
			ConfigDigest:        report.configDigests[platform],
		}
	}
	return reproducibleBuildManifest{
		OCISubjectDigest: report.subjectDigest,
		SourceDateEpoch:  sourceDateEpoch,
		Platforms:        platformManifests,
	}
}

func compareReproducibleBuildReports(reportA, reportB reproducibleBuildReport, platforms []string) error {
	problems := make([]string, 0)
	for _, platform := range platforms {
		if reportA.manifestDigests[platform] != reportB.manifestDigests[platform] {
			problems = append(problems, fmt.Sprintf(
				"Manifest digests (%s): %s != %s",
				platform,
				reportA.manifestDigests[platform],
				reportB.manifestDigests[platform],
			))
		}
		if reportA.configDigests[platform] != reportB.configDigests[platform] {
			problems = append(problems, fmt.Sprintf(
				"Config digests (%s): %s != %s",
				platform,
				reportA.configDigests[platform],
				reportB.configDigests[platform],
			))
		}
	}
	if reportA.subjectDigest != reportB.subjectDigest {
		problems = append(problems, fmt.Sprintf(
			"Non-reproducible OCI export subject digest: %s != %s",
			reportA.subjectDigest,
			reportB.subjectDigest,
		))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func compareReproducibleBuildReportToManifest(report reproducibleBuildReport, manifest reproducibleBuildManifest, platforms []string) error {
	expectedPlatforms := make(map[string]struct{}, len(platforms))
	problems := make([]string, 0)

	for _, platform := range platforms {
		expectedPlatforms[platform] = struct{}{}
		expected, ok := manifest.Platforms[platform]
		if !ok {
			problems = append(problems, fmt.Sprintf("Missing platform digest entry in reproducible build manifest: %s", platform))
			continue
		}
		if report.manifestDigests[platform] != expected.ImageManifestDigest {
			problems = append(problems, fmt.Sprintf(
				"Manifest digests (%s): %s != %s",
				platform,
				expected.ImageManifestDigest,
				report.manifestDigests[platform],
			))
		}
		if report.configDigests[platform] != expected.ConfigDigest {
			problems = append(problems, fmt.Sprintf(
				"Config digests (%s): %s != %s",
				platform,
				expected.ConfigDigest,
				report.configDigests[platform],
			))
		}
	}
	for platform := range manifest.Platforms {
		if _, ok := expectedPlatforms[platform]; !ok {
			problems = append(problems, fmt.Sprintf("Unexpected platform digest entry in reproducible build manifest: %s", platform))
		}
	}
	if report.subjectDigest != manifest.OCISubjectDigest {
		problems = append(problems, fmt.Sprintf(
			"Non-reproducible OCI export subject digest: %s != %s",
			manifest.OCISubjectDigest,
			report.subjectDigest,
		))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func inspectOCIExport(layoutDir string, platforms []string) (reproducibleBuildReport, error) {
	index, err := readOCIIndex(layoutDir)
	if err != nil {
		return reproducibleBuildReport{}, err
	}
	subjectDigest, err := ociSubjectDigestFromIndex(index)
	if err != nil {
		return reproducibleBuildReport{}, err
	}

	manifestDigests := make(map[string]string, len(platforms))
	configDigests := make(map[string]string, len(platforms))
	for _, platform := range platforms {
		manifestDigest, err := ociManifestDigest(layoutDir, index, platform)
		if err != nil {
			return reproducibleBuildReport{}, err
		}
		configDigest, err := ociConfigDigest(layoutDir, manifestDigest)
		if err != nil {
			return reproducibleBuildReport{}, err
		}
		manifestDigests[platform] = manifestDigest
		configDigests[platform] = configDigest
	}

	return reproducibleBuildReport{
		subjectDigest:   subjectDigest,
		manifestDigests: manifestDigests,
		configDigests:   configDigests,
	}, nil
}

func parseReproducibleBuildPlatforms(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	platforms := make([]string, 0, len(parts))
	for _, part := range parts {
		platform := strings.TrimSpace(part)
		if platform == "" {
			continue
		}
		platforms = append(platforms, platform)
	}
	if len(platforms) == 0 {
		return nil, errors.New("WORKCELL_REPRO_PLATFORMS must contain at least one platform")
	}
	return platforms, nil
}

func readOCIIndex(layoutDir string) (map[string]any, error) {
	var index map[string]any
	if err := readJSONFile(filepath.Join(layoutDir, "index.json"), &index); err != nil {
		return nil, err
	}
	return index, nil
}

func ociSubjectDigestFromIndex(index map[string]any) (string, error) {
	manifests, ok := index["manifests"].([]any)
	if !ok || len(manifests) == 0 {
		return "", errors.New("OCI export index does not contain any manifests")
	}
	if allOCIManifestsLackPlatform(manifests) {
		if len(manifests) != 1 {
			return "", errors.New("Expected a single top-level OCI index wrapper entry for multi-platform export")
		}
		digest, ok := ociManifestEntryDigest(manifests[0])
		if !ok || !strings.HasPrefix(digest, "sha256:") {
			return "", fmt.Errorf("Malformed wrapped OCI index digest: %q", digest)
		}
		return digest, nil
	}
	canonical, err := canonicalOCIIndexBytes(index)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalOCIIndexBytes(index map[string]any) ([]byte, error) {
	stripped := stripOCIAnnotations(index)
	return json.Marshal(stripped)
}

func stripOCIAnnotations(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, nested := range typed {
			if key == "annotations" {
				continue
			}
			result[key] = stripOCIAnnotations(nested)
		}
		return result
	case []any:
		result := make([]any, 0, len(typed))
		for _, nested := range typed {
			result = append(result, stripOCIAnnotations(nested))
		}
		return result
	default:
		return value
	}
}

func ociManifestDigest(layoutDir string, index map[string]any, platform string) (string, error) {
	requestedOS, requestedArch, requestedVariant, err := parsePlatformSelector(platform)
	if err != nil {
		return "", err
	}
	manifests, err := ociManifestEntries(layoutDir, index)
	if err != nil {
		return "", err
	}
	matches := make([]string, 0)
	for _, rawManifest := range manifests {
		entry, ok := rawManifest.(map[string]any)
		if !ok {
			continue
		}
		platformInfo, _ := entry["platform"].(map[string]any)
		if platformInfo == nil {
			continue
		}
		if platformValueString(platformInfo, "os") != requestedOS {
			continue
		}
		if platformValueString(platformInfo, "architecture") != requestedArch {
			continue
		}
		if platformValueString(platformInfo, "variant") != requestedVariant {
			continue
		}
		digest, ok := ociManifestEntryDigest(entry)
		if ok {
			matches = append(matches, digest)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("Expected exactly one manifest for %q, found %d", platform, len(matches))
	}
	return matches[0], nil
}

func ociManifestEntries(layoutDir string, index map[string]any) ([]any, error) {
	manifests, ok := index["manifests"].([]any)
	if !ok || len(manifests) == 0 {
		return nil, errors.New("OCI export index does not contain any manifests")
	}
	if !allOCIManifestsLackPlatform(manifests) {
		return manifests, nil
	}
	if len(manifests) != 1 {
		return nil, errors.New("Expected a single top-level OCI index wrapper entry for multi-platform export")
	}
	digest, ok := ociManifestEntryDigest(manifests[0])
	if !ok || !strings.HasPrefix(digest, "sha256:") {
		return nil, fmt.Errorf("Malformed wrapped OCI index digest: %q", digest)
	}
	nestedIndexPath := filepath.Join(layoutDir, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
	var nestedIndex map[string]any
	if err := readJSONFile(nestedIndexPath, &nestedIndex); err != nil {
		return nil, err
	}
	nestedManifests, ok := nestedIndex["manifests"].([]any)
	if !ok || len(nestedManifests) == 0 {
		return nil, errors.New("OCI export index does not contain any manifests")
	}
	return nestedManifests, nil
}

func ociConfigDigest(layoutDir, manifestDigest string) (string, error) {
	if !strings.HasPrefix(manifestDigest, "sha256:") {
		return "", fmt.Errorf("Malformed OCI manifest digest: %q", manifestDigest)
	}
	digest := strings.TrimPrefix(manifestDigest, "sha256:")
	root := filepath.Join(layoutDir, "blobs", "sha256")
	for {
		var manifest map[string]any
		if err := readJSONFile(filepath.Join(root, digest), &manifest); err != nil {
			return "", err
		}
		if config, ok := manifest["config"].(map[string]any); ok {
			configDigest, _ := config["digest"].(string)
			if configDigest == "" {
				return "", fmt.Errorf("Malformed OCI config digest: %q", config)
			}
			return configDigest, nil
		}
		manifests, ok := manifest["manifests"].([]any)
		if !ok || len(manifests) == 0 {
			return "", fmt.Errorf("Malformed OCI index: %q", manifest)
		}
		nextDigest, ok := ociManifestEntryDigest(manifests[0])
		if !ok || !strings.HasPrefix(nextDigest, "sha256:") {
			return "", fmt.Errorf("Malformed OCI manifest digest: %q", nextDigest)
		}
		digest = strings.TrimPrefix(nextDigest, "sha256:")
	}
}

func parsePlatformSelector(platform string) (string, string, string, error) {
	parts := strings.Split(platform, "/")
	switch len(parts) {
	case 2:
		return parts[0], parts[1], "", nil
	case 3:
		return parts[0], parts[1], parts[2], nil
	default:
		return "", "", "", fmt.Errorf("Unsupported platform selector: %q", platform)
	}
}

func platformValueString(platformInfo map[string]any, key string) string {
	value, _ := platformInfo[key].(string)
	return value
}

func ociManifestEntryDigest(entry any) (string, bool) {
	manifest, ok := entry.(map[string]any)
	if !ok {
		return "", false
	}
	digest, ok := manifest["digest"].(string)
	return digest, ok
}

func allOCIManifestsLackPlatform(manifests []any) bool {
	for _, rawManifest := range manifests {
		manifest, ok := rawManifest.(map[string]any)
		if !ok {
			return false
		}
		if _, ok := manifest["platform"]; ok {
			return false
		}
	}
	return true
}
