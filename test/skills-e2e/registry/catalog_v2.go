package registry

// CanonicalCatalog is the single authored semantic ledger. Each row keeps the
// wording, ownership, seam, and minimal execution coordinates together.
func CanonicalCatalog() []CaseDefinition {
	catalog := catalogCases1To24()
	catalog = append(catalog, catalogCases25To48()...)
	catalog = append(catalog, catalogCases49To71()...)
	catalog = append(catalog, catalogCases72To95()...)
	return catalog
}

func catalogCases1To24() []CaseDefinition {
	return []CaseDefinition{
		app(1, "Arbitrary binary bundled files are preserved byte-for-byte.", "loom", "public-lifecycle", loomMinIOBackends()...),
		app(2, "Zero-byte files are valid and retain a stable hash/revision.", "loom", "public-lifecycle", loomMinIOBackends()...),
		app(3, "Nested relative paths are supported.", "loom", "public-lifecycle", loomMinIOBackends()...),
		app(4, "Absolute paths, traversal, NULs, and unsafe separators are rejected.", "loom", "path-domain", loom()),
		app(5, "Duplicate paths are rejected.", "loom", "tree-domain", loom()),
		app(6, "Case-equivalent and Unicode-normalization-equivalent paths are rejected.", "loom", "path-domain", loom()),
		app(7, "File/directory collisions such as bin plus bin/run.sh are rejected.", "loom", "tree-domain", loom()),
		app(8, "Platform-reserved materialization names are rejected.", "loom", "path-domain", loom()),
		app(9, "Local object paths cannot escape the configured store through symlinks.", "fleet", "object-store-conformance", fleet()),
		app(10, "A Skill tree contains exactly one root SKILL.md.", "loom", "skill-tree-domain", loom()),
		app(11, "SKILL.md must be non-executable and valid for Skill metadata parsing.", "loom", "skill-tree-domain", loom()),
		app(12, "Bundled files may be executable or binary.", "loom", "public-lifecycle", loomMinIOBackends()...),
		app(13, "Valid unknown SKILL.md frontmatter is preserved unless identity is explicitly overridden.", "loom", "import-domain", loom()),
		app(14, "Skill name and description must match the referenced root document.", "shared", "write-load-domain", loom(), fleet()),
		app(15, "A description edit publishes a matching new tree and moves description plus tree pointer with one CAS.", "shared", "public-lifecycle-cas", append(loomMinIOBackends(), redisFleet(), postgresFleet())...),
		app(16, "File-count, per-file-size, document-size, path-size, and aggregate-size limits match across FleetDB and Loom.", "shared", "generated-contract", loom(), fleet()),
		app(17, "Generic trees allow the root Skill document plus the maximum bundled-file count without an off-by-one disagreement.", "loom", "skill-tree-domain", loom()),
		app(18, "Manifest ordering does not affect file or tree revisions.", "shared", "identity-vectors", loom(), fleet()),
		app(19, "Caller-supplied response revision fields do not affect derived identity.", "fleet", "model-domain", fleet()),
		app(20, "Same bytes may be reused under different paths without conflating tree identity.", "shared", "identity-vectors", loom(), fleet()),
		app(21, "Same bytes with different media type or executable metadata share bytes but receive distinct file/tree identities.", "shared", "identity-vectors", loom(), fleet()),
		app(22, "A rename creates a new tree while permitting blob reuse.", "shared", "adapter-conformance", loom(), fleet()),
		app(23, "Object references contain no bucket, endpoint, provider key, local path, or credential.", "fleet", "object-store-conformance", fleet()),
		app(24, "Object references are bound to workspace, content hash, and size.", "fleet", "object-identity", fleet()),
	}
}

func catalogCases25To48() []CaseDefinition {
	return []CaseDefinition{
		app(25, "A cross-workspace object reference fails before provider/filesystem I/O.", "fleet", "object-store-conformance", fleet()),
		app(26, "Declared hash mismatch is rejected before publication.", "fleet", "object-api-integration", fleet()),
		app(27, "Declared size mismatch is rejected before publication.", "fleet", "object-api-integration", fleet()),
		app(28, "Missing or corrupt objects prevent tree publication.", "fleet", "object-api-integration", fleet()),
		app(29, "S3 presigned uploads bind exact content length, content type, and SHA-256.", "fleet", "provider-transfer", minioFleet(), gcsFleet()),
		app(30, "Provider object keys use <prefix>/workspaces/<workspace>/sha256/<digest>.", "fleet", "provider-object-key", minioFleet(), gcsFleet()),
		app(31, "The tree revision algorithm is identical across FleetDB and Loom.", "shared", "identity-vectors", loom(), fleet()),
		app(32, "Fleet's canonical contract generates the revision algorithm consumed by both FleetDB and Loom; neither repository owns a handwritten copy.", "shared", "generated-contract", loom(), fleet()),
		app(33, "A same-revision/different-manifest collision fails closed.", "fleet", "storage-conformance", redisFleet(), postgresFleet()),
		app(34, "Downloaded bytes are bounded by declared size and verified by SHA-256 before Loom returns them.", "loom", "transfer-adapter", loomMinIOBackends()...),
		app(35, "Upload tokens are HMAC-authenticated and bind version, kind, workspace, actor, hash, size, media type, object reference, and expiry.", "fleet", "token-service", fleet()),
		app(36, "Tampered, wrong-version, wrong-kind, wrong-actor, wrong-workspace, and expired tokens fail with deliberate error/status behavior.", "fleet", "token-api", fleet()),
		app(37, "Every declared file receives one server-derived transfer grant bound to its canonical object identity.", "fleet", "service-validation", fleet()),
		app(38, "Fleet-relative transfer URLs receive current Fleet authentication and actor headers.", "loom", "transfer-adapter", loom()),
		app(39, "Absolute provider URLs never receive Fleet credentials or the Fleet request editor.", "loom", "transfer-adapter", loom()),
		app(40, "Transfer redirects are disabled.", "shared", "transfer-policy", loom(), fleet()),
		app(41, "Upload grants accept only PUT; download grants accept only GET.", "shared", "grant-validation", loom(), fleet()),
		app(42, "Absolute grants require HTTPS in production, with explicit HTTP opt-in for local MinIO/test endpoints.", "shared", "grant-validation", loom(), fleet()),
		app(43, "Grant expiry is required, future, and within the configured maximum.", "shared", "grant-validation", loom(), fleet()),
		app(44, "Malformed grants fail before any network request.", "shared", "grant-validation", loom(), fleet()),
		app(45, "Signed provider headers are preserved without leaking them into logs.", "loom", "transfer-adapter", loom()),
		app(46, "GCS S3 compatibility excludes incompatible SDK-private headers from SigV4 while retaining required request headers.", "fleet", "gcs-provider", gcsFleet()),
		app(47, "Bytes remain staging data until all objects verify and the tree event is durable.", "fleet", "service-integration", fleet()),
		app(48, "A complete tree is visible atomically or absent; partial manifests are never visible.", "fleet", "projection-conformance", redisFleet(), postgresFleet()),
	}
}

func catalogCases49To71() []CaseDefinition {
	return []CaseDefinition{
		app(49, "Sequential exact publication retry returns the existing tree.", "fleet", "service-integration", fleet()),
		app(50, "Concurrent identical publication produces one logical create event and one first-writer provenance record.", "fleet", "storage-conformance", redisFleet(), postgresFleet()),
		app(51, "First accepted created_by and created_at are deterministic under a publication race.", "fleet", "storage-conformance", redisFleet(), postgresFleet()),
		app(52, "Tree-create idempotency is keyed by workspace and tree revision rather than only a request-generated event ID.", "fleet", "storage-conformance", redisFleet(), postgresFleet()),
		app(53, "Projector replay is idempotent for an identical tree.", "fleet", "projector-conformance", redisFleet(), postgresFleet()),
		app(54, "Projector replay rejects immutable data conflicts.", "fleet", "projector-conformance", redisFleet(), postgresFleet()),
		app(55, "Redis and PostgreSQL provide equivalent tree-create semantics.", "fleet", "storage-conformance", redisFleet(), postgresFleet()),
		app(56, "Skill pointer updates require an expected prior revision.", "fleet", "skill-cas", redisFleet(), postgresFleet()),
		app(57, "Missing and stale pointer preconditions are distinguished.", "fleet", "http-api", fleet()),
		app(58, "A losing Skill CAS leaves the selected Skill unchanged.", "fleet", "skill-cas", redisFleet(), postgresFleet()),
		app(59, "Exact CAS retry reuses the accepted event/result.", "fleet", "skill-cas", redisFleet(), postgresFleet()),
		app(60, "Same-tree metadata edits are not accidentally swallowed as a CAS no-op.", "fleet", "skill-service", fleet()),
		app(61, "Upload failure publishes no tree.", "loom", "adapter-request-trace", loom()),
		app(62, "Tree event append failure publishes no tree but may leave staged objects.", "fleet", "service-failure", fleet()),
		app(63, "Immediate projection failure returns a truthful pending result.", "fleet", "api-projection", fleet()),
		app(64, "A successful Loom WorkspaceFileStore.Publish guarantees that immediate GetTree succeeds.", "loom", "public-lifecycle", loomMinIO()),
		app(65, "If Fleet returns 202 projection_pending, the Fleet adapter waits with a bounded, context-aware policy before reporting success.", "loom", "transfer-adapter", loom()),
		app(66, "Timeout after an ambiguous publish can be retried without duplicate logical creation.", "shared", "public-retry-and-storage", append(loomMinIOBackends(), redisFleet(), postgresFleet())...),
		app(67, "Fleet restart after durable append is recovered by projection replay.", "fleet", "projector-restart", redisFleet(), postgresFleet()),
		app(68, "S3/provider outage before verification creates no manifest/event.", "fleet", "provider-failure", minioFleet(), gcsFleet()),
		app(69, "Store outage during materialization preserves the last safe local projection and surfaces a warning.", "loom", "materializer", loom()),
		app(70, "Integrity, path, collision, and filesystem failures during materialization fail closed rather than replacing the safe projection.", "loom", "materializer", loom()),
		app(71, "A file disappearing between manifest fetch and download fails without partially replacing the materialized Skill.", "loom", "materializer", loom()),
	}
}

func catalogCases72To95() []CaseDefinition {
	return []CaseDefinition{
		na(72, "Existing inline Redis Skills are converted to immutable trees before the strict reader requires file_tree_revision."),
		na(73, "Existing PostgreSQL JSONB Skills are converted likewise."),
		na(74, "Migration preserves exact document and bundle bytes, executable bits, source metadata, and provenance."),
		na(75, "Migration is resumable and idempotent."),
		na(76, "Invalid legacy records stop and report rather than being normalized or silently dropped."),
		na(77, "A preflight proves every Skill has a readable, valid referenced tree before strict cutover."),
		app(78, "Normal runtime is strictly tree-only and contains no legacy inline compatibility decoder.", "fleet", "runtime-storage", redisFleet(), postgresFleet()),
		app(79, "Fleet workspace-file routes can deploy additively before the Skill shape is removed.", "fleet", "release-architecture", fleet()),
		app(80, "The cross-repository release order cannot expose strict Fleet with old Loom or tree-only Loom with old Fleet.", "shared", "release-ci", loom(), fleet()),
		app(81, "Compatibility proofs bind exact Loom, Fleet, corpus, storage-mode, and provider-image revisions.", "loom", "compatibility-ci", loomMinIO(), loomGCS()),
		app(82, "Fleet publication/deployment requires the exact proven paired revisions.", "fleet", "release-deploy-ci", fleet()),
		app(83, "Infrastructure failures such as zero-step Actions billing failures are not reported as product-test failures.", "shared", "ci-reporting", loom(), fleet()),
		app(84, "Interrupted uploads are eventually collectible.", "fleet", "retention", fleet()),
		app(85, "Trees left by losing CAS attempts are retained through a grace period and eventually collectible.", "fleet", "retention", redisFleet(), postgresFleet()),
		app(86, "Current pointers, retained history, active materialization leases, in-progress publication, rollback windows, and holds are GC roots.", "fleet", "retention-roots", redisFleet(), postgresFleet()),
		app(87, "Collection is generation/fence protected against concurrent publication.", "fleet", "retention-concurrency", redisFleet(), postgresFleet()),
		app(88, "Provider deletion is idempotent, retryable, and does not claim success before the result is known.", "fleet", "provider-deletion", minioFleet(), gcsFleet()),
		app(89, "Operators can dry-run collection with object and byte estimates.", "fleet", "retention-operations", fleet()),
		app(90, "S3 lifecycle policy cannot delete objects still live according to FleetDB.", "fleet", "provider-lifecycle", minioFleet(), gcsFleet()),
		app(91, "S3 versioning is treated as operator recovery, not Skill identity or concurrency control.", "fleet", "provider-operations", minioFleet(), gcsFleet()),
		app(92, "Direct S3 download resolution does not require FleetDB to read the entire object before issuing the grant.", "fleet", "provider-download", minioFleet(), gcsFleet()),
		app(93, "If per-download full verification remains, its double-bandwidth cost is documented and benchmarked.", "fleet", "provider-benchmark", minioFleet(), gcsFleet()),
		app(94, "A background scrub can detect and quarantine provider corruption after publication.", "fleet", "provider-scrub", minioFleet(), gcsFleet()),
		app(95, "Metrics distinguish grants, bytes, created/existing/pending publication, visibility latency, CAS conflicts, orphans, provider failures, integrity failures, and reclaimed bytes.", "fleet", "observability", fleet()),
	}
}

func app(id int, behavior, owner, seam string, required ...EvidenceCoordinate) CaseDefinition {
	return CaseDefinition{ID: id, Behavior: behavior, Owner: owner, Seam: seam, RequiredEvidence: required, Decision: DecisionApplicable}
}

func na(id int, behavior string) CaseDefinition {
	return CaseDefinition{ID: id, Behavior: behavior, Owner: "none", Seam: "strict-cutover-decision", Decision: DecisionNotApplicable, Rationale: "The feature is unlaunched; strict fresh-data cutover deliberately requires no legacy compatibility or migration."}
}

func coord(repository Repository, backend Backend, provider Provider) EvidenceCoordinate {
	return EvidenceCoordinate{Repository: repository, Backend: backend, Provider: provider}
}

func loom() EvidenceCoordinate          { return coord(RepositoryLoom, "", "") }
func fleet() EvidenceCoordinate         { return coord(RepositoryFleet, "", "") }
func redisFleet() EvidenceCoordinate    { return coord(RepositoryFleet, BackendRedis, "") }
func postgresFleet() EvidenceCoordinate { return coord(RepositoryFleet, BackendPostgres, "") }
func minioFleet() EvidenceCoordinate    { return coord(RepositoryFleet, "", ProviderMinIO) }
func gcsFleet() EvidenceCoordinate      { return coord(RepositoryFleet, "", ProviderGCS) }
func loomMinIO() EvidenceCoordinate     { return coord(RepositoryLoom, BackendRedis, ProviderMinIO) }
func loomPostgresMinIO() EvidenceCoordinate {
	return coord(RepositoryLoom, BackendPostgres, ProviderMinIO)
}
func loomGCS() EvidenceCoordinate { return coord(RepositoryLoom, BackendRedis, ProviderGCS) }
func loomMinIOBackends() []EvidenceCoordinate {
	return []EvidenceCoordinate{loomMinIO(), loomPostgresMinIO()}
}
