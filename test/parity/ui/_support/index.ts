/**
 * Barrel export for every spec. Specs import from "./_support" and get
 * the canonical set of helpers. Keeps per-spec import lines short.
 */
export {
    preflight,
    type PreflightResult,
    type PreflightCheck,
} from "./preflight";

export {
    openDualTabs,
    gotoBoth,
    gotoViews,
    gotoIssueDetail,
    resetBothBackends,
    snapshotState,
    findFleetIssueByTitle,
    discoverWorkspaceId,
    type DualTabs,
    type BackendState,
    type Backend,
} from "./backends";

export {
    attachFleetNetworkSpy,
    assertRoutingForAction,
    routedFleetRequest,
    type RoutingProof,
    type RoutingVerdict,
} from "./assert-routing";

export {
    captureBothTabs,
    visualDiff,
    structuralDiff,
    saveForensics,
    type CapturedStep,
    type StructuralDiff,
} from "./capture";

export {
    diffIssueLists,
    apiResponseDiff,
    stateSyncDiff,
    timingAssert,
    type ApiResponseDiff,
    type FieldDiff,
    type StateSyncDiff,
    type TimingResult,
} from "./diff";

export {
    ensureSeeded,
    SEED_FIXTURE,
} from "./fixtures";

export {
    REQUIRED_ROUTES,
    REQUIRED_FIELDS,
    recordRoutes,
    normalizeUrlToRoute,
    writeCoverageReport,
    type CoverageRecord,
} from "./coverage";
