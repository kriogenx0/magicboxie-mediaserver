package controllers

// JellyfinServerVersion is the Jellyfin release whose HTTP API contract this
// server implements and advertises to clients. The conformance tests
// (internal/routes/jellyfin_*_test.go) validate responses against that
// release's OpenAPI spec (internal/routes/testdata, refreshed by
// scripts/update-jellyfin-spec.sh) and assert the two stay in step.
const JellyfinServerVersion = "10.11.11"

// jellyfinServerID identifies this server in every DTO that carries a
// ServerId (system info, users, authentication results, items).
const jellyfinServerID = "magicboxie"
