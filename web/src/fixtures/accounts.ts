// The invented accounts and route registry the fixture serves, and accountsServe: a stateful in-memory Accounts and Routes
// service that answers from them. Every id, name, host, profile and fingerprint here is invented, and the fake never holds
// a token.

import { clone, create } from "@bufbuild/protobuf";
import { Code, ConnectError, type ConnectRouter } from "@connectrpc/connect";

import {
  type Account,
  AccountSchema,
  Accounts,
  type AccountsAddRequest,
  AccountsAddResponseSchema,
  type AccountsListRequest,
  AccountsListResponseSchema,
  type AccountsRemoveRequest,
  AccountsRemoveResponseSchema,
  type AccountsRenameRequest,
  AccountsRenameResponseSchema,
  Routes,
  type RouteRegistry,
  RouteRegistrySchema,
  type RoutesGetRequest,
  RoutesGetResponseSchema,
  type RoutesReplaceRequest,
  RoutesReplaceResponseSchema,
  type RoutesSetContextAccountRequest,
  RoutesSetContextAccountResponseSchema,
} from "../gen/orrery/v1/accounts_pb";

// accounts are the invented accounts the fixture starts from, in the shape of round 8's accounts page: three Claude Max
// accounts and one Claude Pro account. created is fixed, in unix seconds, one day apart from 12 November 2026.
export const accounts: readonly Account[] = [
  create(AccountSchema, {
    id: "max-1",
    name: "max-1",
    provider: "anthropic",
    plan: "Claude Max",
    fingerprint: "3f9a1c0e7b25",
    created: 1794441600n,
  }),
  create(AccountSchema, {
    id: "max-2",
    name: "max-2",
    provider: "anthropic",
    plan: "Claude Max",
    fingerprint: "b0217ea4d9c8",
    created: 1794528000n,
  }),
  create(AccountSchema, {
    id: "max-3",
    name: "max-3",
    provider: "anthropic",
    plan: "Claude Max",
    fingerprint: "5d0c44f1e3a7",
    created: 1794614400n,
  }),
  create(AccountSchema, {
    id: "pro-1",
    name: "pro-1",
    provider: "anthropic",
    plan: "Claude Pro",
    fingerprint: "e81f05b6c924",
    created: 1794700800n,
  }),
];

// registry is the invented route registry the fixture starts from: the seats, and two contexts. personal is the default,
// and sends its forward seats the account max-1; work has no account, so its seats send the session's own login.
export const registry: RouteRegistry = create(RouteRegistrySchema, {
  defaultContext: "personal",
  seats: [
    { id: "anthropic", authMode: "forward", upstream: "https://api.anthropic.com" },
    { id: "anthropic-haiku", authMode: "forward", upstream: "https://api.anthropic.com", model: "example-haiku" },
    {
      id: "work-bedrock",
      authMode: "sigv4",
      awsProfile: "example-profile",
      awsRegion: "example-region-1",
      model: "example-bedrock",
    },
    {
      id: "chatgpt-luna",
      authMode: "inject",
      upstream: "http://127.0.0.1:8317",
      model: "example-luna",
      apiKeyEnvFile: "example-luna.env",
      billing: "subscription",
      speed: "fast",
    },
    {
      id: "openrouter-sol",
      authMode: "inject",
      upstream: "https://models.example.test",
      model: "example-sol",
      apiKeyEnvFile: "example-sol.env",
      billing: "metered",
      speed: "standard",
    },
  ],
  contexts: [
    {
      id: "personal",
      defaultSeat: "anthropic",
      models: { "example-haiku": "anthropic-haiku", "example-luna": "chatgpt-luna" },
      subagents: { defaultSeat: "anthropic", agentTypes: { Explore: "anthropic-haiku" } },
      account: "max-1",
    },
    { id: "work", defaultSeat: "work-bedrock", account: "" },
  ],
});

// Requests names the request each method of the fake carries, by the method's service and name.
interface Requests {
  "Accounts.Add": AccountsAddRequest;
  "Accounts.List": AccountsListRequest;
  "Accounts.Rename": AccountsRenameRequest;
  "Accounts.Remove": AccountsRemoveRequest;
  "Routes.Get": RoutesGetRequest;
  "Routes.Replace": RoutesReplaceRequest;
  "Routes.SetContextAccount": RoutesSetContextAccountRequest;
}

// Method is a method the fake serves, named as its service and method: "Accounts.List".
export type Method = keyof Requests;

// Call is one call the fake received: its method and the request it carried.
export type Call = { [M in Method]: { readonly method: M; readonly request: Requests[M] } }[Method];

// AccountsSeed is the state the fake starts from: the accounts it holds and the route registry it serves.
export interface AccountsSeed {
  readonly accounts: readonly Account[];
  readonly registry: RouteRegistry;
}

// AccountsServe is the fake: its handlers are registered on a router, its calls are recorded, and its next calls can be
// refused or dropped.
export interface AccountsServe {
  // register serves Accounts and Routes on router, so a createRouterTransport answers their calls from this state.
  register(router: ConnectRouter): void;
  // calls holds every call received, in order, refused and dropped calls included.
  readonly calls: readonly Call[];
  // refuseNext makes the next call of method fail with code and message, changing nothing. Each refusal and drop is taken
  // by one call, in the order they were given.
  refuseNext(method: Method, code: Code, message: string): void;
  // dropNext makes the next call of method fail as a deadline does, changing nothing.
  dropNext(method: Method): void;
}

// Outcome is what a queued call is answered with instead of being served: a refusal, or a drop.
type Outcome = { readonly code: Code; readonly message: string } | "drop";

// accountsServe returns the fake, starting from seed. The fake copies the seed, so no change it makes reaches the seed.
export function accountsServe(seed: AccountsSeed): AccountsServe {
  let accounts: Account[] = seed.accounts.map((account) => clone(AccountSchema, account));
  let registry: RouteRegistry = clone(RouteRegistrySchema, seed.registry);
  const calls: Call[] = [];
  const queued = new Map<Method, Outcome[]>();
  let minted = 0;

  // arrive records a call, then fails it when an outcome is queued for its method.
  function arrive<M extends Method>(method: M, request: Requests[M]): void {
    calls.push({ method, request } as Call);
    const outcome = queued.get(method)?.shift();
    if (outcome === undefined) {
      return;
    }
    if (outcome === "drop") {
      throw new ConnectError("deadline exceeded", Code.DeadlineExceeded);
    }
    throw new ConnectError(outcome.message, outcome.code);
  }

  // served wraps a handler so that the call is recorded and may be refused or dropped before the handler answers it.
  function served<M extends Method, R>(method: M, handle: (request: Requests[M]) => R): (request: Requests[M]) => R {
    return (request) => {
      arrive(method, request);
      return handle(request);
    };
  }

  // mint returns the next invented id and fingerprint. Both come from a count of the adds, never from a token.
  function mint(): { id: string; fingerprint: string } {
    minted += 1;
    return {
      id: minted.toString(16).padStart(32, "0"),
      fingerprint: (0xf00d00000000 + minted).toString(16),
    };
  }

  // nextCreated is one second after the newest account's creation, so an account added now sorts last.
  function nextCreated(): bigint {
    return accounts.reduce((newest, account) => (account.created > newest ? account.created : newest), 0n) + 1n;
  }

  // accountNamed is the stored account with id. It fails NotFound when no account holds id.
  function accountNamed(id: string): Account {
    const account = accounts.find((candidate) => candidate.id === id);
    if (account === undefined) {
      throw new ConnectError("account: unknown id", Code.NotFound);
    }
    return account;
  }

  // inOrder is the accounts in the order List answers them: created ascending, then by id.
  function inOrder(): Account[] {
    return [...accounts].sort((left, right) => {
      if (left.created !== right.created) {
        return left.created < right.created ? -1 : 1;
      }
      return left.id < right.id ? -1 : left.id > right.id ? 1 : 0;
    });
  }

  const add = (request: AccountsAddRequest) => {
    const { id, fingerprint } = mint();
    // The token is not kept: the account holds the fingerprint the fake invented for it.
    const account = create(AccountSchema, {
      id,
      name: request.name,
      provider: request.provider,
      plan: request.plan,
      fingerprint,
      created: nextCreated(),
    });
    accounts.push(account);
    return create(AccountsAddResponseSchema, { account: clone(AccountSchema, account) });
  };

  const list = () =>
    create(AccountsListResponseSchema, { accounts: inOrder().map((account) => clone(AccountSchema, account)) });

  const rename = (request: AccountsRenameRequest) => {
    const account = accountNamed(request.id);
    account.name = request.name;
    return create(AccountsRenameResponseSchema, { account: clone(AccountSchema, account) });
  };

  // remove fails FailedPrecondition, naming the first context in registry order, when a context uses the account.
  const remove = (request: AccountsRemoveRequest) => {
    const account = accountNamed(request.id);
    const user = registry.contexts.find((context) => context.account === account.id);
    if (user !== undefined) {
      throw new ConnectError(`account: in use by context ${user.id}`, Code.FailedPrecondition);
    }
    accounts = accounts.filter((candidate) => candidate !== account);
    return create(AccountsRemoveResponseSchema);
  };

  const get = () => create(RoutesGetResponseSchema, { registry: clone(RouteRegistrySchema, registry) });

  const replace = (request: RoutesReplaceRequest) => {
    registry = clone(RouteRegistrySchema, request.registry ?? create(RouteRegistrySchema));
    return create(RoutesReplaceResponseSchema, { registry: clone(RouteRegistrySchema, registry) });
  };

  // setContextAccount fails NotFound for a context no registry holds, and for an account no account holds. An empty
  // account is no account, and the context then sends its caller's own login.
  const setContextAccount = (request: RoutesSetContextAccountRequest) => {
    const context = registry.contexts.find((candidate) => candidate.id === request.context);
    if (context === undefined) {
      throw new ConnectError("context: unknown id", Code.NotFound);
    }
    if (request.account !== "") {
      accountNamed(request.account);
    }
    context.account = request.account;
    return create(RoutesSetContextAccountResponseSchema, { registry: clone(RouteRegistrySchema, registry) });
  };

  return {
    register(router) {
      router.service(Accounts, {
        add: served("Accounts.Add", add),
        list: served("Accounts.List", list),
        rename: served("Accounts.Rename", rename),
        remove: served("Accounts.Remove", remove),
      });
      router.service(Routes, {
        get: served("Routes.Get", get),
        replace: served("Routes.Replace", replace),
        setContextAccount: served("Routes.SetContextAccount", setContextAccount),
      });
    },
    calls,
    refuseNext(method, code, message) {
      queued.set(method, [...(queued.get(method) ?? []), { code, message }]);
    },
    dropNext(method) {
      queued.set(method, [...(queued.get(method) ?? []), "drop"]);
    },
  };
}
