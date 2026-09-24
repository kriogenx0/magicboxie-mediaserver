#!/usr/bin/env bash
# Refreshes internal/routes/testdata/jellyfin-openapi.json, the trimmed copy of
# Jellyfin's published OpenAPI spec that the API-conformance tests
# (internal/routes/jellyfin_*_test.go) validate this server against.
#
#   scripts/update-jellyfin-spec.sh            # default: 10.11.11
#   scripts/update-jellyfin-spec.sh 10.11.12   # a specific release
#
# The full spec is ~2MB; this keeps every path/method (for route checks) but
# only the request/response schemas of the operations the tests exercise,
# stripped of prose. Keep the version in step with the one the server
# advertises in controllers.JellyfinServerVersion (a test enforces that).
set -euo pipefail

VERSION="${1:-10.11.11}"
URL="https://repo.jellyfin.org/files/openapi/stable/jellyfin-openapi-${VERSION}.json"
OUT="$(cd "$(dirname "$0")/.." && pwd)/internal/routes/testdata/jellyfin-openapi.json"

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
curl -fsSL --max-time 120 "$URL" -o "$TMP"

python3 - "$TMP" "$OUT" "$URL" <<'PY'
import json, sys

src, out, url = sys.argv[1:4]
spec = json.load(open(src))

# Operations whose request/response schemas the tests validate against.
DETAILED = {
    "GetPublicSystemInfo", "GetSystemInfo", "GetPingSystem", "PostPingSystem",
    "GetPublicUsers", "AuthenticateUserByName", "GetCurrentUser", "GetUserById",
    "GetQuickConnectEnabled", "GetBrandingOptions",
    "GetUserViews", "GetItems", "GetItem", "GetLatestMedia", "GetResumeItems", "GetNextUp",
    "GetPlaybackInfo", "GetPostedPlaybackInfo",
    "ReportPlaybackStart", "ReportPlaybackProgress", "ReportPlaybackStopped",
    "PostCapabilities", "PostFullCapabilities",
}
PROSE = {"description", "summary", "example", "examples", "title", "externalDocs", "tags", "security"}

def strip(node):
    if isinstance(node, dict):
        return {k: strip(v) for k, v in node.items() if k not in PROSE and not k.startswith("x-")}
    if isinstance(node, list):
        return [strip(v) for v in node]
    return node

def refs(node, found):
    if isinstance(node, dict):
        for k, v in node.items():
            if k == "$ref" and isinstance(v, str) and v.startswith("#/components/schemas/"):
                found.add(v.rsplit("/", 1)[1])
            else:
                refs(v, found)
    elif isinstance(node, list):
        for v in node:
            refs(v, found)

paths, needed = {}, set()
for path, item in spec["paths"].items():
    kept = {}
    for method, op in item.items():
        if method not in ("get", "post", "put", "delete", "head", "patch"):
            continue
        entry = {"operationId": op.get("operationId"), "deprecated": op.get("deprecated", False)}
        params = [{"name": p["name"], "in": p["in"], "required": p.get("required", False)}
                  for p in op.get("parameters", [])]
        if op.get("operationId") in DETAILED:
            entry["parameters"] = [strip(p) for p in op.get("parameters", [])]
            if "requestBody" in op:
                entry["requestBody"] = strip(op["requestBody"])
            entry["responses"] = strip(op.get("responses", {}))
            refs(entry, needed)
        else:
            entry["parameters"] = params
            entry["responses"] = {code: {} for code in op.get("responses", {})}
        kept[method] = entry
    paths[path] = kept

schemas, queue = {}, sorted(needed)
while queue:
    name = queue.pop()
    if name in schemas:
        continue
    schemas[name] = strip(spec["components"]["schemas"][name])
    more = set()
    refs(schemas[name], more)
    queue.extend(sorted(more - set(schemas)))

doc = {
    "openapi": spec["openapi"],
    "info": {"title": spec["info"]["title"], "version": spec["info"]["version"]},
    "x-source": url,
    "paths": paths,
    "components": {"schemas": schemas},
}
with open(out, "w") as f:
    json.dump(doc, f, indent=1, sort_keys=True)
    f.write("\n")
print(f"wrote {out}: {len(paths)} paths, {len(schemas)} schemas (Jellyfin {doc['info']['version']})")
PY
