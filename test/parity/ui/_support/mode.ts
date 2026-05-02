export type ParityMode = "dual" | "fleet-only";

export function parityMode(): ParityMode {
  const raw = (process.env.PARITY_MODE ?? "").trim().toLowerCase();
  if (raw === "dual") {
    return "dual";
  }
  return "fleet-only";
}

export function isFleetOnlyMode(): boolean {
  return parityMode() === "fleet-only";
}
