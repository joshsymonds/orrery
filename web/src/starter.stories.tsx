// The stories of Starter: the prompt first, then the place it runs, in its six states. The states a Propose leaves are
// reached by typing a prompt and pressing ⏎ once the Starter has drawn, against a fake Picker.Propose that answers the
// way the story says. The typed-folder story opens with its folder and host given. Every host and folder here is invented.

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import type { VNode } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import type { Clients } from "./actions";
import type { Stories } from "./catalog/story";
import { createClients } from "./client";
import { Picker, ProposeResponseSchema, StartProposalSchema } from "./gen/orrery/v1/orrery_pb";
import { Starter as StarterView } from "./starter";

// reachable are the hosts that answer, which the Starter offers. brine is not among them.
const reachable = ["halyard", "dock"];

// askedPrompt is the invented prompt a story types before it presses ⏎.
const askedPrompt = "Fix the flaky api test";

// Answer is what a story's fake Picker.Propose does: it proposes the places, fails, or never answers.
type Answer = { kind: "proposes"; proposals: { host: string; dir: string }[] } | { kind: "fails" } | { kind: "holds" };

const noop = () => {};

// clientsFor makes the clients of a fake whose Picker.Propose does what answer says. Each mount makes its own fake.
function clientsFor(answer: Answer): Clients {
  return createClients(
    createRouterTransport((router) => {
      router.service(Picker, {
        propose: async () => {
          if (answer.kind === "fails") {
            throw new ConnectError("the search backend is down", Code.Unavailable);
          }
          if (answer.kind === "holds") {
            return new Promise<never>(() => {});
          }
          return create(ProposeResponseSchema, {
            proposals: answer.proposals.map((proposal) => create(StartProposalSchema, proposal)),
          });
        },
      });
    }),
  );
}

interface OpenedProps {
  answer: Answer;
  // prompt is typed into the Starter once it has drawn, when the story gives one.
  prompt?: string;
  // press is whether the story presses ⏎ in the prompt after the prompt is typed.
  press?: boolean;
  folder?: string;
  host?: string;
}

// Opened draws the Starter over clients for answer. Once it has drawn, it types the prompt the story gives, and presses
// ⏎ in it when the story says to. Typing and pressing are two renders, so the press reads the prompt the typing set.
function Opened({ answer, prompt, press = false, folder, host }: OpenedProps): VNode {
  const [clients] = useState(() => clientsFor(answer));
  const root = useRef<HTMLDivElement | null>(null);
  const [typed, setTyped] = useState(false);
  useEffect(() => {
    const box = root.current?.querySelector<HTMLInputElement>("input");
    if (box && prompt !== undefined) {
      box.value = prompt;
      box.dispatchEvent(new Event("input", { bubbles: true }));
    }
    setTyped(true);
  }, []);
  useEffect(() => {
    if (typed && press) {
      root.current?.querySelector("input")?.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    }
  }, [typed]);
  return (
    <div ref={root}>
      <StarterView
        clients={clients}
        hosts={reachable}
        folder={folder}
        host={host}
        sheet={false}
        onStarted={noop}
        onClose={noop}
      />
    </div>
  );
}

export const Starter: Stories = {
  prompt: () => <Opened answer={{ kind: "holds" }} />,
  loading: () => <Opened answer={{ kind: "holds" }} prompt={askedPrompt} press />,
  several: () => (
    <Opened
      answer={{
        kind: "proposes",
        proposals: [
          { host: "halyard", dir: "/work/api" },
          { host: "dock", dir: "/work/web" },
        ],
      }}
      prompt={askedPrompt}
      press
    />
  ),
  none: () => (
    <Opened answer={{ kind: "proposes", proposals: [{ host: "brine", dir: "/work/ops" }] }} prompt={askedPrompt} press />
  ),
  failed: () => <Opened answer={{ kind: "fails" }} prompt={askedPrompt} press />,
  "typed-folder": () => <Opened answer={{ kind: "holds" }} prompt={askedPrompt} folder="/work/api" host="dock" />,
};
