export type ParityMode = "dual" | "fleet-only";

export function parityMode(): ParityMode {
    const raw = (process.env.PARITY_MODE ?? "").trim().toLowerCase();
    if (raw === "fleet-only" || process.env.PARITY_FLEET_ONLY === "1") {
        return "fleet-only";
    }
    return "dual";
}

export function isFleetOnlyMode(): boolean {
    return parityMode() === "fleet-only";
}
