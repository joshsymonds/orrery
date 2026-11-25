package store

import "time"

// The kinds of a design step, the values of design_steps' kind column.
const (
	StepResearch  = "research"
	StepReproduce = "reproduce"
	StepDiscovery = "discovery"
	StepClosure   = "closure"
)

// The dispositions of a design item, the values of design_dispositions' disposition column.
const (
	DispositionAdopted             = "adopted"
	DispositionRejected            = "rejected"
	DispositionDeferred            = "deferred"
	DispositionOpen                = "open"
	DispositionRevisedAfterClosure = "revised_after_closure"
	DispositionAcceptedAsRisk      = "accepted_as_risk"
)

// The makers of a stage call, the values of the maker columns of design_questions, design_dispositions and
// design_closure_skips. A question's maker is "" until it is answered.
const (
	StageMakerOwnerServe        = "owner_serve"
	StageMakerOwnerConversation = "owner_conversation"
	StageMakerConversation      = "conversation"
)

// ContractDraft is a contract the stage is writing and has not yet proposed. Body is the EpicContract in protobuf JSON,
// and Decisions are its decisions. Design is "" for none. Created and Updated are when the draft was first and last
// written.
type ContractDraft struct {
	ID, Design, Repository, Title, Body string
	Decisions                           []ContractDecision
	Words, Writer                       string
	Created, Updated                    time.Time
}

// DesignStep is one step the stage recorded against a design. Kind is one of the Step constants, and Subject is the
// research question or the reproduce command.
type DesignStep struct {
	Step, Design, Kind, Subject, Repository, Revision, Delta string
	Created                                                  time.Time
}

// StageMaker is who made a stage call. Kind is one of the StageMaker constants, and Quote, Reason and Delegation are the
// call's quote, maker reason and delegation.
type StageMaker struct {
	Kind, Quote, Reason, Delegation string
}

// DesignQuestion is one question the stage asked about a design, numbered by Seq within it. Contract is "" when the
// question names no contract. Answered is the zero time until the question is answered, and Maker is the zero StageMaker
// until then.
type DesignQuestion struct {
	Design, Contract, Text string
	Seq                    int64
	Options                []string
	Recommendation, Asker  string
	Asked                  time.Time
	Answer                 string
	Accepted               bool
	Maker                  StageMaker
	Answerer               string
	Answered               time.Time
}

// DesignDisposition is one disposition of a design item, numbered by Seq within the design. The newest disposition of
// an item is the item's disposition. Disposition is one of the Disposition constants.
type DesignDisposition struct {
	Design, Item, Disposition string
	Seq                       int64
	Reason, Boundary          string
	Maker                     StageMaker
	Session                   string
	At                        time.Time
}

// ClosureSkip is the record that a design's closure was skipped, with its reason. Each design has at most one.
type ClosureSkip struct {
	Design, Reason string
	Maker          StageMaker
	Session        string
	At             time.Time
}
