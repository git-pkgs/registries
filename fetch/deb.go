package fetch

import (
	"fmt"
	"maps"
	"regexp"
	"strings"

	"github.com/git-pkgs/registries"
)

var (
	// debReleasePattern matches a release as scanners such as trivy and
	// syft write it in the distro qualifier, with an optional point
	// release: debian-12, debian-12.4, ubuntu-22.04.
	debReleasePattern = regexp.MustCompile(`^(debian-\d+|ubuntu-\d+\.\d+)(?:\.\d+)?$`)

	// debCodenames are the codenames of the Debian and Ubuntu releases,
	// which the deb client records as an artifact's distro. Other releases
	// can be given by codename.
	debCodenames = map[string]string{
		"debian-8": "jessie", "debian-9": "stretch", "debian-10": "buster",
		"debian-11": "bullseye", "debian-12": "bookworm", "debian-13": "trixie",
		"debian-14":    "forky",
		"ubuntu-16.04": "xenial", "ubuntu-18.04": "bionic", "ubuntu-20.04": "focal",
		"ubuntu-22.04": "jammy", "ubuntu-24.04": "noble", "ubuntu-24.10": "oracular",
		"ubuntu-25.04": "plucky", "ubuntu-25.10": "questing",
	}
)

// debQualifiers returns qualifiers with a distro given as a release, such
// as debian-12, replaced by its codename. qualifiers itself is left as it
// is.
func debQualifiers(qualifiers map[string]string) map[string]string {
	match := debReleasePattern.FindStringSubmatch(qualifiers["distro"])
	if match == nil {
		return qualifiers
	}
	codename, ok := debCodenames[match[1]]
	if !ok {
		return qualifiers
	}
	qualifiers = maps.Clone(qualifiers)
	qualifiers["distro"] = codename
	return qualifiers
}

// debVersion returns the version of versions that version names. A version
// given without its epoch, as PURLs often give it, matches the one with that
// number. The epoch qualifier, as trivy writes it, picks the epoch over the
// one in version. Without either, a number listed with two epochs is
// refused rather than one picked, even when one of them has none.
func debVersion(versions []registries.Version, version, epoch string) (registries.Version, error) {
	given, number := debSplitEpoch(version)
	if epoch == "" {
		epoch = given
	}
	var found []registries.Version
	for _, v := range versions {
		e, n := debSplitEpoch(v.Number)
		if n != number {
			continue
		}
		if epoch != "" && debEpoch(e) != debEpoch(epoch) {
			continue
		}
		found = append(found, v)
	}
	switch len(found) {
	case 0:
		return registries.Version{}, ErrNotFound
	case 1:
		return found[0], nil
	}
	numbers := make([]string, len(found))
	for i, v := range found {
		numbers[i] = v.Number
	}
	return registries.Version{}, fmt.Errorf("%w: %s is listed as %s; give the epoch",
		ErrAmbiguousVersion, version, strings.Join(numbers, ", "))
}

// debSplitEpoch splits the epoch off a Debian version, returning "" for
// one without. A version has a colon only after its epoch.
func debSplitEpoch(version string) (epoch, rest string) {
	if e, r, ok := strings.Cut(version, ":"); ok {
		return e, r
	}
	return "", version
}

// debEpoch returns epoch with a missing epoch as 0, which it means.
func debEpoch(epoch string) string {
	if trimmed := strings.TrimLeft(epoch, "0"); trimmed != "" {
		return trimmed
	}
	return "0"
}
