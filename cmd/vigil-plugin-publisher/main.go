package main

import (
	"crypto/ed25519"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PayCal-Technologies/vigil-public/internal/buildinfo"
	"github.com/PayCal-Technologies/vigil-public/internal/cli"
	"github.com/PayCal-Technologies/vigil-public/internal/output"
	"github.com/PayCal-Technologies/vigil-public/internal/plugins"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return cli.ExitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return cli.ExitSuccess
	case "version", "--version":
		return version(args[1:])
	case "keygen":
		return keygen(args[1:])
	case "key-id":
		return keyID(args[1:])
	case "sign":
		return sign(args[1:])
	case "inspect":
		return inspect(args[1:])
	case "verify":
		return verify(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
		printUsage(os.Stderr)
		return cli.ExitUsage
	}
}

func version(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return cli.ExitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: vigil-plugin-publisher version [--json]")
		return cli.ExitUsage
	}
	info := buildinfo.Current()
	if *jsonOut {
		return reportSuccess("version", true, map[string]any{"build": info}, "")
	}
	fmt.Printf(
		"vigil-plugin-publisher %s commit=%s built=%s dirty=%s go=%s os=%s arch=%s\n",
		info.Version,
		info.Commit,
		info.BuildDate,
		info.Dirty,
		info.GoVersion,
		info.OS,
		info.Arch,
	)
	return cli.ExitSuccess
}

type repeatedPaths []string

func (paths *repeatedPaths) String() string {
	return strings.Join(*paths, ",")
}

func (paths *repeatedPaths) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("path must not be empty")
	}
	*paths = append(*paths, value)
	return nil
}

func keygen(args []string) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	privatePath := fs.String("private-key", "", "new private key path")
	publicPath := fs.String("public-key", "", "new public key path")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return cli.ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(*privatePath) == "" || strings.TrimSpace(*publicPath) == "" {
		fmt.Fprintln(os.Stderr, "Usage: vigil-plugin-publisher keygen --private-key PATH --public-key PATH [--json]")
		return cli.ExitUsage
	}
	privateAbsolute, err := filepath.Abs(*privatePath)
	if err != nil {
		return reportError("keygen", *jsonOut, err)
	}
	publicAbsolute, err := filepath.Abs(*publicPath)
	if err != nil {
		return reportError("keygen", *jsonOut, err)
	}
	if privateAbsolute == publicAbsolute {
		return reportError("keygen", *jsonOut, plugins.InvalidError("generate publisher key", "private and public key paths must differ"))
	}
	for _, path := range []string{privateAbsolute, publicAbsolute} {
		if _, err := os.Lstat(path); err == nil {
			return reportError("keygen", *jsonOut, plugins.BlockedError("generate publisher key", "refusing to replace existing file "+path))
		} else if !os.IsNotExist(err) {
			return reportError("keygen", *jsonOut, err)
		}
	}
	generated, err := plugins.GeneratePublisherKey()
	if err != nil {
		return reportError("keygen", *jsonOut, err)
	}
	defer clear(generated.PrivateKey)
	encodedPrivate, err := plugins.EncodePublisherPrivateKey(generated.PrivateKey)
	if err != nil {
		return reportError("keygen", *jsonOut, err)
	}
	encodedPublic, err := plugins.EncodePublisherPublicKey(generated.PublicKey)
	if err != nil {
		return reportError("keygen", *jsonOut, err)
	}
	if err := plugins.WriteExclusiveFile(privateAbsolute, []byte(encodedPrivate+"\n"), 0o600); err != nil {
		return reportError("keygen", *jsonOut, err)
	}
	if err := plugins.WriteExclusiveFile(publicAbsolute, []byte(encodedPublic+"\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "private key was created and retained at %s\n", privateAbsolute)
		return reportError("keygen", *jsonOut, err)
	}
	payload := map[string]any{
		"key_id": generated.KeyID, "algorithm": plugins.PublisherAlgorithm,
		"private_key_path": privateAbsolute, "public_key_path": publicAbsolute,
	}
	return reportSuccess("keygen", *jsonOut, payload, fmt.Sprintf("generated %s", generated.KeyID))
}

func keyID(args []string) int {
	fs := flag.NewFlagSet("key-id", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	privatePath := fs.String("private-key", "", "private key path")
	publicPath := fs.String("public-key", "", "public key path")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return cli.ExitUsage
	}
	if fs.NArg() != 0 || (strings.TrimSpace(*privatePath) == "") == (strings.TrimSpace(*publicPath) == "") {
		fmt.Fprintln(os.Stderr, "Usage: vigil-plugin-publisher key-id (--private-key PATH|--public-key PATH) [--json]")
		return cli.ExitUsage
	}
	var publicKey ed25519.PublicKey
	var err error
	if strings.TrimSpace(*privatePath) != "" {
		var privateKey ed25519.PrivateKey
		privateKey, err = plugins.ReadPublisherPrivateKeyFile(*privatePath)
		if err == nil {
			defer clear(privateKey)
			publicKey = privateKey.Public().(ed25519.PublicKey)
		}
	} else {
		publicKey, err = plugins.ReadPublisherPublicKeyFile(*publicPath)
	}
	if err != nil {
		return reportError("key-id", *jsonOut, err)
	}
	keyID := plugins.PublisherKeyID(publicKey)
	return reportSuccess(
		"key-id",
		*jsonOut,
		map[string]any{"key_id": keyID, "algorithm": plugins.PublisherAlgorithm},
		keyID,
	)
}

func sign(args []string) int {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	indexPath := fs.String("index", "", "unsigned or partially signed index path")
	privatePath := fs.String("private-key", "", "private key path")
	outputPath := fs.String("output", "", "new signed index path")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return cli.ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(*indexPath) == "" ||
		strings.TrimSpace(*privatePath) == "" || strings.TrimSpace(*outputPath) == "" {
		fmt.Fprintln(os.Stderr, "Usage: vigil-plugin-publisher sign --index PATH --private-key PATH --output PATH [--json]")
		return cli.ExitUsage
	}
	document, err := plugins.ReadIndexDraftFile(*indexPath)
	if err != nil {
		return reportError("sign", *jsonOut, err)
	}
	privateKey, err := plugins.ReadPublisherPrivateKeyFile(*privatePath)
	if err != nil {
		return reportError("sign", *jsonOut, err)
	}
	defer clear(privateKey)
	document, err = plugins.SignIndexDraft(document, privateKey)
	if err != nil {
		return reportError("sign", *jsonOut, err)
	}
	encoded, err := plugins.EncodeIndexDraft(document)
	if err != nil {
		return reportError("sign", *jsonOut, err)
	}
	outputAbsolute, err := filepath.Abs(*outputPath)
	if err != nil {
		return reportError("sign", *jsonOut, err)
	}
	if err := plugins.WriteExclusiveFile(outputAbsolute, encoded, 0o644); err != nil {
		return reportError("sign", *jsonOut, err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	digest, err := plugins.IndexDocumentDigest(document)
	if err != nil {
		return reportError("sign", *jsonOut, err)
	}
	thresholdFilled := len(document.Signatures) >= document.Signed.SignatureThreshold
	payload := map[string]any{
		"key_id": plugins.PublisherKeyID(publicKey), "output": outputAbsolute,
		"index_digest": digest, "signature_count": len(document.Signatures),
		"signature_threshold": document.Signed.SignatureThreshold,
		"threshold_filled":    thresholdFilled,
	}
	return reportSuccess("sign", *jsonOut, payload, fmt.Sprintf(
		"signed %s signatures=%d/%d",
		outputAbsolute,
		len(document.Signatures),
		document.Signed.SignatureThreshold,
	))
}

func inspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	indexPath := fs.String("index", "", "unsigned or partially signed index path")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return cli.ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(*indexPath) == "" {
		fmt.Fprintln(os.Stderr, "Usage: vigil-plugin-publisher inspect --index PATH [--json]")
		return cli.ExitUsage
	}
	document, err := plugins.ReadIndexDraftFile(*indexPath)
	if err != nil {
		return reportError("inspect", *jsonOut, err)
	}
	digest, err := plugins.IndexDocumentDigest(document)
	if err != nil {
		return reportError("inspect", *jsonOut, err)
	}
	keyIDs := make([]string, 0, len(document.Signatures))
	for _, signature := range document.Signatures {
		keyIDs = append(keyIDs, signature.KeyID)
	}
	payload := map[string]any{
		"index_digest": digest, "release_count": len(document.Signed.Plugins),
		"signature_count": len(document.Signatures), "signature_threshold": document.Signed.SignatureThreshold,
		"threshold_filled": len(document.Signatures) >= document.Signed.SignatureThreshold,
		"key_ids":          keyIDs,
	}
	return reportSuccess("inspect", *jsonOut, payload, fmt.Sprintf(
		"releases=%d signatures=%d/%d",
		len(document.Signed.Plugins),
		len(document.Signatures),
		document.Signed.SignatureThreshold,
	))
}

func verify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	indexPath := fs.String("index", "", "threshold-signed index path")
	at := fs.String("at", "", "verification time in RFC3339 format")
	jsonOut := fs.Bool("json", false, "JSON output")
	var publicPaths repeatedPaths
	fs.Var(&publicPaths, "public-key", "trusted public key path (repeatable)")
	if err := fs.Parse(args); err != nil {
		return cli.ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(*indexPath) == "" || len(publicPaths) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: vigil-plugin-publisher verify --index PATH --public-key PATH [--public-key PATH ...] [--at RFC3339] [--json]")
		return cli.ExitUsage
	}
	document, err := plugins.ReadIndexDraftFile(*indexPath)
	if err != nil {
		return reportError("verify", *jsonOut, err)
	}
	if err := plugins.ValidateIndex(document); err != nil {
		return reportError("verify", *jsonOut, err)
	}
	verificationTime := time.Now().UTC()
	if strings.TrimSpace(*at) != "" {
		verificationTime, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(*at))
		if err != nil {
			return reportError("verify", *jsonOut, plugins.InvalidError("verify plugin index", "--at must be RFC3339"))
		}
	}
	store := plugins.PublisherStore{
		SchemaVersion: plugins.PublisherSchemaVersion,
		Keys:          []plugins.PublisherKey{},
		RevokedKeyIDs: []string{},
	}
	for _, path := range publicPaths {
		publicKey, readErr := plugins.ReadPublisherPublicKeyFile(path)
		if readErr != nil {
			return reportError("verify", *jsonOut, readErr)
		}
		encoded, encodeErr := plugins.EncodePublisherPublicKey(publicKey)
		if encodeErr != nil {
			return reportError("verify", *jsonOut, encodeErr)
		}
		store.Keys = append(store.Keys, plugins.PublisherKey{
			KeyID:      plugins.PublisherKeyID(publicKey),
			Name:       filepath.Base(path),
			Algorithm:  plugins.PublisherAlgorithm,
			PublicKey:  encoded,
			ApprovedAt: time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
		})
	}
	verified, err := plugins.VerifyIndex(document, store, verificationTime)
	if err != nil {
		return reportError("verify", *jsonOut, err)
	}
	return reportSuccess(
		"verify",
		*jsonOut,
		map[string]any{
			"index_digest": verified.IndexDigest,
			"signer_ids":   verified.SignerIDs,
			"threshold":    verified.Document.Signed.SignatureThreshold,
			"verified_at":  verificationTime.UTC().Format(time.RFC3339Nano),
		},
		fmt.Sprintf("verified %s signers=%d threshold=%d", verified.IndexDigest, len(verified.SignerIDs), verified.Document.Signed.SignatureThreshold),
	)
}

func reportSuccess(command string, jsonOut bool, data map[string]any, human string) int {
	if jsonOut {
		return writeEnvelope(command, cli.ExitSuccess, data)
	}
	fmt.Println(human)
	return cli.ExitSuccess
}

func reportError(command string, jsonOut bool, err error) int {
	exitCode := plugins.ExitCode(err)
	if jsonOut {
		return writeEnvelope(command, exitCode, map[string]any{"error": err.Error()})
	}
	fmt.Fprintln(os.Stderr, err)
	return exitCode
}

func writeEnvelope(command string, exitCode int, data any) int {
	now := time.Now().UTC()
	envelope := output.EnvelopeFromPayload(command, exitCode, now, now, data)
	if err := output.WriteEnvelope(os.Stdout, envelope); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return cli.ExitInternal
	}
	return exitCode
}

func printUsage(destination *os.File) {
	fmt.Fprintln(destination, "Vigil offline plugin-index publisher")
	fmt.Fprintln(destination)
	fmt.Fprintln(destination, "Usage:")
	fmt.Fprintln(destination, "  vigil-plugin-publisher version")
	fmt.Fprintln(destination, "  vigil-plugin-publisher keygen --private-key PATH --public-key PATH")
	fmt.Fprintln(destination, "  vigil-plugin-publisher key-id (--private-key PATH|--public-key PATH)")
	fmt.Fprintln(destination, "  vigil-plugin-publisher sign --index PATH --private-key PATH --output PATH")
	fmt.Fprintln(destination, "  vigil-plugin-publisher inspect --index PATH")
	fmt.Fprintln(destination, "  vigil-plugin-publisher verify --index PATH --public-key PATH [--public-key PATH ...]")
}
