// The host the fixture slices report: its name and its load stats, as Host.Status answers them.

import { create } from "@bufbuild/protobuf";

import { HostStatusResponseSchema, type HostStatusResponse } from "../gen/orrery/v1/orrery_pb";

// host is the Host.Status answer.
export const host: HostStatusResponse = create(HostStatusResponseSchema, {
  host: "halyard",
  stats: [
    { label: "cpu", value: "12%", percent: 12, level: "ok" },
    { label: "ram", value: "14G", percent: 44, level: "ok" },
    { label: "disk", value: "49%", percent: 49, level: "ok" },
    { label: "net", value: "↓1M ↑298K", level: "ok" },
  ],
});
