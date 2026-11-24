# The contract stage

The contract stage runs through Orrery's own tools, records and rules, so any conversation can run it with no plugin. Call design_guide before the stage and follow these rules. A refusal names the rule it enforces.

## Research

- design_research asks one question about a repository at a revision, for a design. A read-only scout answers it. Use it for questions about code.
- design_reproduce runs a command in a repository at a revision, for a design, through a read-only test runner step. Use it to show what a command does at a commit.
- design_stage reads a design's stage: each research and reproduction step with its status and, once it returns, its report. It also holds the questions, the ledger and each steelman pass's verdict. Read the reports there.

## Questions

- design_ask stores a question on a design, about a contract or about none. Give its text, its options, and a recommendation that is one of the options. At least one option is required. The question stays unanswered until design_answer answers it.
- design_answer answers a question. Its maker says where the answer came from, and the maker carries the evidence its kind needs:
  - owner_serve: the owner answered through serve. The caller resolves to no session, and the answer is all it needs.
  - owner_conversation: the owner answered in the conversation. The caller resolves to a session, and the maker quotes the owner's words.
  - conversation: the conversation decides for the owner. The caller resolves to a session, and the maker gives its reason and the delegation it rests on.
- accept: true answers with the question's recommendation and takes no answer text. Otherwise the answer's words are required. A question already answered is refused.

## Drafts and contracts

- contract_draft stores a draft of a contract under an id, in the design named by design when one is given. A draft has no versions and is not a contract until contract_propose proposes it under the same id. A draft gets no length warning.
- contract_propose stores the contract in the file at path as version 1, in state proposed. contract_revise stores the file at path as a new version of a stored contract, by id. Both read the file in the shape contract_propose reads: one JSON object in protobuf JSON field names, with an optional decisions array.
- contract_propose and contract_revise refuse a checks text whose Quality Bar line is not verbatim. Each returns length warnings: lines above the checks at or past the warning threshold give one warning, and lines at or past the strong threshold give one stronger warning that asks for a cut or a split instead. Length never refuses a contract.

## The steelman

design_steelman runs one steelman pass on a design. Its budget is one counted discovery pass and at most one counted closure pass, and a closure may be recorded skipped in its place.

- With no counted discovery, the pass is the discovery pass on the design's packet: each contract at its newest version and each draft, with their open decisions.
- With a counted discovery and no closure, the pass is the closure pass on the packet, the frozen ledger and a delta. The delta is required: what the revision changed since discovery.
- With skipClosure, the closure is recorded skipped, with its reason and its maker. It needs a counted discovery and no closure step.

A pass counts when its step ends with a return whose verdict is not BLOCKED. A BLOCKED return, or a step that ends with no return, does not count, so it does not spend the budget. Run the pass again. A skip is refused once a closure step exists.

No pass starts while a discovery or closure step is still running. Once the budget is spent, by a counted closure or a skipped closure, the design is refused.

## Dispositions

The ledger holds the findings of the counted discovery return, then the concerns of the counted closure return. Each item's disposition is open until design_dispose stores one for it, made by a maker as design_answer's makers are.

Before the freeze, design_dispose takes:

- adopted;
- rejected, with a reason;
- deferred, with a boundary the item is deferred to;
- open.

The ledger freezes at closure: once a closure step exists or a closure is skipped. After the freeze, design_dispose takes only:

- revised_after_closure, with a reason, for an item that is open;
- accepted_as_risk, with a reason, for an item that is open.

Any other disposition is refused, and so is an item that already has one after the freeze.

## Accepting a contract

contract_accept accepts a stored contract by id, with the owner's words, which are optional. It is refused when:

- no contract holds the id;
- the contract is not proposed;
- a decision has no answer, and the refusal names the open decision ids;
- origin's main cannot be fetched, so the base cannot be stored.

## The contract's sections

A contract renders its sections in this order, its checks last:

1. The title and its Approved line.
2. What you asked for.
3. What could go wrong, and how much we care: each failure with how bad it is and what we do, then the level of care.
4. Things you did not ask for: each addition, or None.
5. What will be true when done: each requirement with how we'll know it.
6. What I'm assuming: each premise with what happens if it is wrong.
7. What we won't do.
8. How, and why not the other ways: the approach, then each alternative with why not and when to reconsider.
9. What leaves this machine or can't be undone: each release step with its target and undo, then what follows.
10. Decisions I need from you: each open decision with its recommendation and reason, or None.
11. Checks the machines run: the checks, one per line.

## The Quality Bar

The checks carry the Quality Bar as a line of their own, verbatim, beginning "Quality Bar: ". Propose and revise refuse checks without it.

Quality Bar: Failing, low-quality, or bad code is unacceptable, but failure to meet a mythic platonic ideal of code or cover literally every imaginable edge case is NOT itself a defect. This bar is FIXED for every epic; write it verbatim — never elicit it, strengthen it, or make it a per-project preference. A defect is exactly one of: a Requirement's named evidence not met; a Must Not Ship entry present; a change outside the task's owned files; a change the contract did not ask for; a violation of the implementer contract's mechanical floor (a suppressed check, a weakened or tautological test, dead code, an unhandled error); an assertion its declared gate finds does not hold; or a security or data-loss failure with a reachable precondition that the change itself introduces. Everything else the orchestrator or a reviewer notices is an observation — it may be recorded in the Decision Log and the report; it never becomes work during the run. The craftsmanship asked of the implementer is one line: simple, foundational, secure; match the surrounding code.
