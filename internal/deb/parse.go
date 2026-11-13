package deb

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxLineSize bounds one index line. bookworm main has a 75,649-byte
// Provides: line.
const maxLineSize = 1 << 20

// record is one stanza of a Packages index, with the fields the client uses.
type record struct {
	name          string
	version       string
	arch          string
	source        string
	maintainer    string
	description   string
	homepage      string
	section       string
	filename      string
	sha256        string
	size          int64
	installedSize int64
	depends       string
	preDepends    string

	// Where the stanza was read.
	suite     string
	component string
	distro    string
}

// parseStanzas reads a Packages index as a stream, calling fn for each
// stanza. Field names are matched case-insensitively and folded lines are
// joined, as deb822(5) asks of readers.
func parseStanzas(r io.Reader, fn func(record)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, maxLineSize)
	var rec record
	var field string // the field a continuation line belongs to
	inStanza := false
	lineNo := 0

	finish := func() error {
		if !inStanza {
			return nil
		}
		if rec.name == "" || rec.version == "" {
			return fmt.Errorf("%w: stanza ending at line %d has no Package or Version", ErrMalformedIndex, lineNo)
		}
		fn(rec)
		rec, field, inStanza = record{}, "", false
		return nil
	}

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if err := finish(); err != nil {
				return err
			}
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if !inStanza {
				return fmt.Errorf("%w: line %d continues no field", ErrMalformedIndex, lineNo)
			}
			rec.continueField(field, strings.TrimSpace(line))
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return fmt.Errorf("%w: line %d is not a field", ErrMalformedIndex, lineNo)
		}
		inStanza = true
		field = strings.ToLower(key)
		if err := rec.set(field, strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("%w: line %d: %w", ErrMalformedIndex, lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedIndex, err)
	}
	return finish()
}

func (rec *record) set(field, value string) error {
	var err error
	switch field {
	case "package":
		rec.name = value
	case "version":
		rec.version = value
	case "architecture":
		rec.arch = value
	case "source":
		rec.source = value
	case "maintainer":
		rec.maintainer = value
	case "description":
		rec.description = value
	case "homepage":
		rec.homepage = value
	case "section":
		rec.section = value
	case "filename":
		rec.filename = value
	case "sha256":
		rec.sha256 = value
	case "size":
		rec.size, err = strconv.ParseInt(value, 10, 64)
	case "installed-size":
		rec.installedSize, err = strconv.ParseInt(value, 10, 64)
	case "depends":
		rec.depends = value
	case "pre-depends":
		rec.preDepends = value
	}
	return err
}

// continueField appends a folded line to the relationship fields. Other
// multi-line fields, such as Description, keep their first line.
func (rec *record) continueField(field, value string) {
	switch field {
	case "depends":
		rec.depends = joinFolded(rec.depends, value)
	case "pre-depends":
		rec.preDepends = joinFolded(rec.preDepends, value)
	}
}

func joinFolded(s, more string) string {
	if s == "" {
		return more
	}
	return s + " " + more
}

// fileSum is a Release file's SHA256 entry for one index file.
type fileSum struct {
	sha256 string
	size   int64
}

// release is the part of a Release or InRelease file the client uses.
type release struct {
	codename string
	suite    string
	// files maps a path under dists/<suite>/ to its SHA256 entry.
	files map[string]fileSum
}

// parseRelease reads a Release file, or an InRelease file, whose
// clearsigned text is read without checking the signature.
func parseRelease(data []byte) (*release, error) {
	rel := &release{files: map[string]fileSum{}}
	scanner := bufio.NewScanner(bytes.NewReader(clearsignedText(data)))
	scanner.Buffer(nil, maxLineSize)
	field := ""
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if field != "sha256" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) != 3 {
				return nil, fmt.Errorf("%w: Release SHA256 entry %q", ErrMalformedIndex, line)
			}
			size, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: Release SHA256 entry %q", ErrMalformedIndex, line)
			}
			rel.files[parts[2]] = fileSum{sha256: strings.ToLower(parts[0]), size: size}
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		field = strings.ToLower(key)
		switch field {
		case "codename":
			rel.codename = strings.TrimSpace(value)
		case "suite":
			rel.suite = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedIndex, err)
	}
	return rel, nil
}

// clearsignedText returns the signed text of an OpenPGP clearsigned
// message, with dash escapes removed, or data unchanged if it is not one.
func clearsignedText(data []byte) []byte {
	const begin, sig = "-----BEGIN PGP SIGNED MESSAGE-----", "-----BEGIN PGP SIGNATURE-----"
	if !bytes.HasPrefix(data, []byte(begin)) {
		return data
	}
	// Armor headers such as Hash: end at the first blank line.
	_, body, ok := bytes.Cut(data, []byte("\n\n"))
	if !ok {
		return nil
	}
	if i := bytes.Index(body, []byte("\n"+sig)); i >= 0 {
		body = body[:i+1]
	}
	lines := bytes.SplitAfter(body, []byte("\n"))
	for i, line := range lines {
		lines[i] = bytes.TrimPrefix(line, []byte("- "))
	}
	return bytes.Join(lines, nil)
}

// distro is the codename a suite's packages are recorded under:
// bookworm-updates and bookworm-security are bookworm.
func distro(codename string) string {
	name, _, _ := strings.Cut(codename, "-")
	return name
}

// dependency is one relationship from Depends or Pre-Depends.
type dependency struct {
	name        string
	requirement string
}

// parseDependencies reads relationship fields. Of alternatives ("a | b")
// the first is kept, as apt tries it first; architecture qualifiers
// (zlib1g:any), architecture restrictions ([linux-any]) and build profiles
// (<!nocheck>) are dropped.
func parseDependencies(fields ...string) []dependency {
	var deps []dependency
	for _, field := range fields {
		for _, group := range strings.Split(field, ",") {
			first, _, _ := strings.Cut(group, "|")
			first = cutBetween(cutBetween(first, "[", "]"), "<", ">")
			name, requirement, _ := strings.Cut(first, "(")
			name = strings.TrimSpace(name)
			name, _, _ = strings.Cut(name, ":")
			if name == "" {
				continue
			}
			requirement = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(requirement), ")"))
			deps = append(deps, dependency{name: name, requirement: requirement})
		}
	}
	return deps
}

// cutBetween removes every open...close span from s.
func cutBetween(s, open, close string) string {
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], close)
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+len(close):]
	}
}

// safePoolPath reports whether a Filename stays under the archive root:
// relative, without dot segments, escapes or URL syntax.
func safePoolPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, `\?#%:`) {
		return false
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
