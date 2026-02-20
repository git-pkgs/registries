package client

import "fmt"

// URLBuilder constructs URLs for a registry.
type URLBuilder interface {
	Registry(name, version string) string
	Download(name, version string) string
	Documentation(name, version string) string
	PURL(name, version string) string
}

// BaseURLs provides a default URLBuilder implementation.
type BaseURLs struct {
	RegistryFn      func(name, version string) string
	DownloadFn      func(name, version string) string
	DocumentationFn func(name, version string) string
	PURLFn          func(name, version string) string
}

func (b *BaseURLs) Registry(name, version string) string {
	if b.RegistryFn != nil {
		return b.RegistryFn(name, version)
	}
	return ""
}

func (b *BaseURLs) Download(name, version string) string {
	if b.DownloadFn != nil {
		return b.DownloadFn(name, version)
	}
	return ""
}

func (b *BaseURLs) Documentation(name, version string) string {
	if b.DocumentationFn != nil {
		return b.DocumentationFn(name, version)
	}
	return ""
}

func (b *BaseURLs) PURL(name, version string) string {
	if b.PURLFn != nil {
		return b.PURLFn(name, version)
	}
	return fmt.Sprintf("pkg:%s/%s", "generic", name)
}

// BuildURLs returns a map of all non-empty URLs for a package.
// Keys are "registry", "download", "docs", and "purl".
func BuildURLs(urls URLBuilder, name, version string) map[string]string {
	result := make(map[string]string)
	if v := urls.Registry(name, version); v != "" {
		result["registry"] = v
	}
	if v := urls.Download(name, version); v != "" {
		result["download"] = v
	}
	if v := urls.Documentation(name, version); v != "" {
		result["docs"] = v
	}
	if v := urls.PURL(name, version); v != "" {
		result["purl"] = v
	}
	return result
}
