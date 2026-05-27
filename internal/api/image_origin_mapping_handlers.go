package api

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// publicRegistryShape matches DNS hostname-with-optional-port, lowercase.
// Mirror-side image names use lower-case by Docker convention; the spec
// intentionally rejects '/' so callers cannot smuggle a path into the
// registry field.
var publicRegistryShape = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?$`)

var (
	errImageNameRequired       = errors.New("image_name is required")
	errImageNameContainsScheme = errors.New("image_name must not contain '://'")
	errImageNameContainsTag    = errors.New("image_name must not contain ':' (tags are passed through automatically)")
	errImageNameContainsDigest = errors.New("image_name must not contain '@' (digests are not supported)")
	errImageNameContainsSpace  = errors.New("image_name must not contain whitespace")
	errImageNameTooLong        = errors.New("image_name exceeds 512 characters")
	errPublicRegistryRequired  = errors.New("public_registry is required")
	errPublicRegistryHasPath   = errors.New("public_registry must be a hostname only, no '/'")
	errPublicRegistryShape     = errors.New("public_registry must be a valid hostname (a-z, 0-9, '.', '-') with optional ':port'")
	errPublicRegistryTooLong   = errors.New("public_registry exceeds 253 characters")
	errNotesTooLong            = errors.New("notes exceeds 2048 characters")
)

func validateImageName(s string) error {
	if s == "" {
		return errImageNameRequired
	}
	if strings.Contains(s, "://") {
		return errImageNameContainsScheme
	}
	if strings.ContainsAny(s, " \t\n\r") {
		return errImageNameContainsSpace
	}
	if strings.Contains(s, ":") {
		return errImageNameContainsTag
	}
	if strings.Contains(s, "@") {
		return errImageNameContainsDigest
	}
	if len(s) > 512 {
		return errImageNameTooLong
	}
	return nil
}

func validatePublicRegistry(s string) error {
	if s == "" {
		return errPublicRegistryRequired
	}
	if strings.Contains(s, "/") {
		return errPublicRegistryHasPath
	}
	if len(s) > 253 {
		return errPublicRegistryTooLong
	}
	if !publicRegistryShape.MatchString(s) {
		return errPublicRegistryShape
	}
	return nil
}

func validateNotes(s *string) error {
	if s == nil {
		return nil
	}
	if len(*s) > 2048 {
		return errNotesTooLong
	}
	return nil
}

// callerIDFromContext extracts the caller's username from the request context.
// Returns "" when no caller is present (e.g. in tests without auth middleware).
func callerIDFromContext(ctx context.Context) string {
	if c := auth.CallerFromContext(ctx); c != nil {
		return c.Username
	}
	return ""
}
