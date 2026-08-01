package plugins

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxIndexBytes = 4 * 1024 * 1024
	fetchTimeout  = 30 * time.Second
)

type IndexLoadOptions struct {
	HTTPClient *http.Client
	Now        time.Time
}

type LoadedIndex struct {
	Verified  VerifiedIndex `json:"verified"`
	Source    string        `json:"source"`
	LocalRoot string        `json:"local_root,omitempty"`
	BaseURL   string        `json:"base_url,omitempty"`
}

type AcquiredPlugin struct {
	Path               string        `json:"path"`
	Release            IndexRelease  `json:"release"`
	Artifact           IndexArtifact `json:"artifact"`
	IndexDigest        string        `json:"index_digest"`
	SignatureThreshold int           `json:"signature_threshold"`
	SignerIDs          []string      `json:"signer_ids"`
	Source             string        `json:"source"`
}

func LoadVerifiedIndex(ctx context.Context, layout Layout, source string, options IndexLoadOptions) (LoadedIndex, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	data, identity, localRoot, baseURL, err := loadIndexSource(ctx, source, options.HTTPClient)
	if err != nil {
		return LoadedIndex{}, err
	}
	document, err := DecodeIndex(data)
	if err != nil {
		return LoadedIndex{}, err
	}
	publishers, err := ReadPublishers(layout.PublisherPath)
	if err != nil {
		return LoadedIndex{}, err
	}
	verified, err := VerifyIndex(document, publishers, options.Now)
	if err != nil {
		return LoadedIndex{}, err
	}
	return LoadedIndex{
		Verified:  verified,
		Source:    identity,
		LocalRoot: localRoot,
		BaseURL:   baseURL,
	}, nil
}

func AcquireIndexedPlugin(ctx context.Context, layout Layout, index LoadedIndex, selected SelectedRelease, client *http.Client) (AcquiredPlugin, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	data, err := loadIndexArtifact(ctx, index, selected.Artifact, client)
	if err != nil {
		return AcquiredPlugin{}, err
	}
	if int64(len(data)) != selected.Artifact.Size {
		return AcquiredPlugin{}, pluginError(
			ErrorBlocked,
			"acquire plugin",
			"artifact size %d differs from signed size %d",
			len(data),
			selected.Artifact.Size,
		)
	}
	if digest := digestBytes(data); digest != selected.Artifact.Digest {
		return AcquiredPlugin{}, pluginError(ErrorBlocked, "acquire plugin", "artifact digest differs from signed index")
	}
	downloadRoot := filepath.Join(layout.Root, ".downloads")
	if err := os.MkdirAll(downloadRoot, 0o700); err != nil {
		return AcquiredPlugin{}, wrapPluginError(ErrorInternal, "create plugin download directory", err)
	}
	file, err := os.CreateTemp(downloadRoot, "vigil-plugin-"+selected.Release.ID+"-*")
	if err != nil {
		return AcquiredPlugin{}, wrapPluginError(ErrorInternal, "create plugin download", err)
	}
	path := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o700); err != nil {
		return AcquiredPlugin{}, wrapPluginError(ErrorInternal, "secure plugin download", err)
	}
	if _, err := file.Write(data); err != nil {
		return AcquiredPlugin{}, wrapPluginError(ErrorInternal, "write plugin download", err)
	}
	if err := file.Sync(); err != nil {
		return AcquiredPlugin{}, wrapPluginError(ErrorInternal, "sync plugin download", err)
	}
	if err := file.Close(); err != nil {
		return AcquiredPlugin{}, wrapPluginError(ErrorInternal, "close plugin download", err)
	}
	if digest, err := FileDigest(path); err != nil || digest != selected.Artifact.Digest {
		return AcquiredPlugin{}, pluginError(ErrorInternal, "acquire plugin", "staged artifact digest verification failed")
	}
	keep = true
	return AcquiredPlugin{
		Path:               path,
		Release:            selected.Release,
		Artifact:           selected.Artifact,
		IndexDigest:        index.Verified.IndexDigest,
		SignatureThreshold: index.Verified.Document.Signed.SignatureThreshold,
		SignerIDs:          append([]string(nil), index.Verified.SignerIDs...),
		Source:             index.Source,
	}, nil
}

func RemoveAcquiredPlugin(acquired AcquiredPlugin) error {
	if strings.TrimSpace(acquired.Path) == "" {
		return nil
	}
	return os.Remove(acquired.Path)
}

func loadIndexSource(ctx context.Context, source string, client *http.Client) ([]byte, string, string, string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, "", "", "", pluginError(ErrorInvalid, "load plugin index", "index source is required")
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return nil, "", "", "", wrapPluginError(ErrorInvalid, "load plugin index", err)
	}
	if parsed.Scheme == "" {
		absolute, err := filepath.Abs(source)
		if err != nil {
			return nil, "", "", "", wrapPluginError(ErrorInvalid, "load plugin index", err)
		}
		data, err := readBoundedRegularFile(absolute, maxIndexBytes)
		if err != nil {
			return nil, "", "", "", err
		}
		return data, absolute, filepath.Dir(absolute), "", nil
	}
	if err := validateHTTPSURL(parsed, "index"); err != nil {
		return nil, "", "", "", err
	}
	data, finalURL, err := fetchHTTPS(ctx, parsed, maxIndexBytes, client)
	if err != nil {
		return nil, "", "", "", err
	}
	return data, finalURL.String(), "", finalURL.String(), nil
}

func loadIndexArtifact(ctx context.Context, index LoadedIndex, artifact IndexArtifact, client *http.Client) ([]byte, error) {
	reference, err := url.Parse(artifact.URL)
	if err != nil {
		return nil, wrapPluginError(ErrorInvalid, "load plugin artifact", err)
	}
	if reference.IsAbs() {
		if err := validateHTTPSURL(reference, "artifact"); err != nil {
			return nil, err
		}
		data, _, err := fetchHTTPS(ctx, reference, artifact.Size, client)
		return data, err
	}
	if index.LocalRoot != "" {
		root, err := filepath.Abs(index.LocalRoot)
		if err != nil {
			return nil, wrapPluginError(ErrorInternal, "load plugin artifact", err)
		}
		candidate, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(reference.Path)))
		if err != nil {
			return nil, wrapPluginError(ErrorInvalid, "load plugin artifact", err)
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, pluginError(ErrorBlocked, "load plugin artifact", "relative artifact escapes index directory")
		}
		return readBoundedRegularFile(candidate, artifact.Size)
	}
	base, err := url.Parse(index.BaseURL)
	if err != nil || base == nil {
		return nil, pluginError(ErrorInternal, "load plugin artifact", "verified index has no source base")
	}
	resolved := base.ResolveReference(reference)
	if err := validateHTTPSURL(resolved, "artifact"); err != nil {
		return nil, err
	}
	data, _, err := fetchHTTPS(ctx, resolved, artifact.Size, client)
	return data, err
}

func readBoundedRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, pluginError(ErrorMissing, "read plugin source", "file does not exist: %s", filepath.Base(path))
	}
	if err != nil {
		return nil, wrapPluginError(ErrorInternal, "read plugin source", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, pluginError(ErrorBlocked, "read plugin source", "source must be a regular non-symlink file")
	}
	if info.Size() <= 0 || info.Size() > limit {
		return nil, pluginError(ErrorBlocked, "read plugin source", "source size is outside the signed limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, wrapPluginError(ErrorInternal, "read plugin source", err)
	}
	if int64(len(data)) > limit {
		return nil, pluginError(ErrorBlocked, "read plugin source", "source exceeds the signed limit")
	}
	return data, nil
}

func fetchHTTPS(ctx context.Context, target *url.URL, limit int64, client *http.Client) ([]byte, *url.URL, error) {
	if err := validateHTTPSURL(target, "source"); err != nil {
		return nil, nil, err
	}
	if limit <= 0 || limit > maxPluginBytes {
		return nil, nil, pluginError(ErrorInvalid, "fetch plugin source", "invalid download limit")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, nil, wrapPluginError(ErrorInvalid, "fetch plugin source", err)
	}
	request.Header.Set("Accept", "application/json, application/octet-stream")
	response, err := secureHTTPClient(client).Do(request)
	if err != nil {
		var policyError *PluginError
		if errors.As(err, &policyError) {
			return nil, nil, policyError
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, wrapPluginError(ErrorInterrupted, "fetch plugin source", err)
		}
		return nil, nil, wrapPluginError(ErrorMissing, "fetch plugin source", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, pluginError(ErrorMissing, "fetch plugin source", "HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, nil, pluginError(ErrorBlocked, "fetch plugin source", "response exceeds the signed limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, nil, wrapPluginError(ErrorMissing, "fetch plugin source", err)
	}
	if int64(len(data)) > limit {
		return nil, nil, pluginError(ErrorBlocked, "fetch plugin source", "response exceeds the signed limit")
	}
	return data, response.Request.URL, nil
}

func secureHTTPClient(provided *http.Client) *http.Client {
	if provided != nil {
		copy := *provided
		copy.CheckRedirect = secureRedirectPolicy(copy.CheckRedirect)
		if copy.Timeout <= 0 || copy.Timeout > fetchTimeout {
			copy.Timeout = fetchTimeout
		}
		return &copy
	}
	return &http.Client{
		Timeout:       fetchTimeout,
		CheckRedirect: secureRedirectPolicy(nil),
	}
}

func secureRedirectPolicy(previous func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return pluginError(ErrorBlocked, "fetch plugin source", "too many redirects")
		}
		if err := validateHTTPSURL(request.URL, "redirect"); err != nil {
			return err
		}
		if previous != nil {
			return previous(request, via)
		}
		return nil
	}
}

func validateHTTPSURL(target *url.URL, kind string) error {
	if target == nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Fragment != "" {
		return pluginError(ErrorBlocked, "validate plugin "+kind, "URL must be HTTPS without credentials or a fragment")
	}
	return nil
}
