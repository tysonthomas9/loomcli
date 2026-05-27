export type FleetDBMode = "dual" | "fleet-only";

export function fleetdbMode(): FleetDBMode {
  const raw = (process.env.PLAYWRIGHT_FLEETDB_MODE ?? "").trim().toLowerCase();
  if (raw === "dual") {
    return "dual";
  }
  return "fleet-only";
}

export function isFleetOnlyMode(): boolean {
  return fleetdbMode() === "fleet-only";
}
