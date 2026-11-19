// The host the fixture slices report: its name and its load stats, as Host.Status answers them.

import { create } from "@bufbuild/protobuf";

import { HostStatusResponseSchema, type HostStatusResponse } from "../gen/orrery/v1/orrery_pb";

// host is the Host.Status answer.
export const host: HostStatusResponse = create(HostStatusResponseSchema, {
  host: "vermissian",
  stats: [
    { label: "cpu", value: "34%", percent: 34, level: "ok" },
    { label: "mem", value: "61%", percent: 61, level: "ok" },
    { label: "load", value: "2.1", level: "" },
    { label: "disk", value: "48%", percent: 48, level: "ok" },
  ],
});
