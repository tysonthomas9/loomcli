export function humanInputAdvancesEpoch(payload) {
  return payload.type === "mousePressed" || payload.type === "mouseWheel" || payload.type === "keyDown";
}
