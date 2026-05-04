package mcp

import "github.com/sthalbert/longue-vue/internal/api"

// redactCloudAccount returns a copy of the input with credential fields
// stripped. MCP exposes cloud accounts at the read scope; AK/SK never
// leave the vm-collector path (ADR-0015 §5). The CloudAccount struct
// already omits SK fields entirely — only AccessKey is plaintext on the
// wire, so this helper just nils that pointer.
//
//nolint:gocritic // hugeParam: deliberate value receiver to make a copy of the struct.
func redactCloudAccount(in api.CloudAccount) api.CloudAccount {
	in.AccessKey = nil
	return in
}
