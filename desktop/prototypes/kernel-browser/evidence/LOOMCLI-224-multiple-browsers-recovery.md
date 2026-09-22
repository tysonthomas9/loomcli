# LOOMCLI-224 Multiple Browsers and Recovery

Date: 2026-09-21 (America/Los_Angeles)

Status: **passed with material resource cost**.

## Isolation and switching

Two independently labelled containers ran simultaneously:

| App | Live view | CDP | Media TCP |
|---|---:|---:|---:|
| app-a / Research | 61080 | 61222 | 56000 |
| app-b / Operations | 62080 | 62222 | 56100 |

All host mappings were on `127.0.0.1`. The profiles retained different
`localStorage` values (`research` and `operations`) after reread. Switching the
POC UI from Research to Operations replaced the iframe stream and app-b emitted
`KERNEL_CONNECTED` then `KERNEL_PLAYING`.

The final UI keeps both browser containers and profiles alive but loads only
the active iframe. With one stream per browser during measurement, app-a and
app-b each consumed about 45-49% CPU and 1.34-1.87 GiB. After the app-b viewer
closed, Kernel logged its session disconnected and app-b fell to 0.15% CPU
while still using 1.25 GiB. These are point-in-time Docker statistics under
Rosetta, not a benchmark.

## Recovery

A VM process loss left both task-owned containers at `Exited (255)`. The
runtime harness refused to overwrite their names. Its stop operation selected
both ownership labels, removed only those two containers, and start recreated
both at CDP-ready status. Normal stop/start had already been exercised when
changing WebRTC from UDP to TCP mux.

The final configuration uses TCP because the original UDP mapping did not
produce a viable ICE path through Colima/Lima. Fixed per-browser media ports
must be replaced by a real allocator before production.
