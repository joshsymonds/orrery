package store

// The actions of a contract row write, the values of contract_row_writes' action column.
const (
	RowActionWrite  = "write"
	RowActionRemove = "remove"
	RowActionKeep   = "keep"
	RowActionStrike = "strike"
)

// The makers of a design log line, the values of design_log's maker column.
const (
	LogMakerOwner        = "owner"
	LogMakerConversation = "conversation"
	LogMakerOrreryd      = "orreryd"
)

// DesignApproach is one approach a design weighed, numbered by Seq within Design. Research names the steps the approach
// rests on, and Recommended is whether the design recommends it. At is unix seconds.
type DesignApproach struct {
	Design                                     string
	Seq                                        int64
	Name, Shape, Cost, Wins, Loses, Reconsider string
	Research                                   []string
	Recommended                                bool
	Reason, Session                            string
	At                                         int64
}

// ApproachChoice is a choice of an approach a design made. Approach is the Seq of the DesignApproach chosen. Maker is
// one of the StageMaker constants, and Quote, MakerReason and Delegation are the maker columns design_questions holds.
// At is unix seconds.
type ApproachChoice struct {
	Design                                       string
	Seq, Approach                                int64
	Words, Maker, Quote, MakerReason, Delegation string
	Session                                      string
	At                                           int64
}

// DesignDelegation is the owner's delegation of a design's decisions, numbered by Seq within Design. Kinds are the kinds
// it names, and Message is the owner's delegating message whole. At is unix seconds.
type DesignDelegation struct {
	Design  string
	Seq     int64
	Kinds   []string
	Message string
	Session string
	At      int64
}

// DesignConversation is a conversation a design has had with the owner, numbered by Seq within Design. The newest one
// names the design's working conversation. Previous is the sessions row id of the conversation it replaced, and "" for
// the first. At is unix seconds.
type DesignConversation struct {
	Design   string
	Seq      int64
	Session  string
	Previous string
	At       int64
}

// DesignLogLine is one line of a design's log, numbered by Seq within Design. Maker is one of the LogMaker constants,
// What says what the line records, and IDs are the records it concerns. At is unix seconds.
type DesignLogLine struct {
	Design  string
	Seq     int64
	At      int64
	Maker   string
	Session string
	What    string
	IDs     []string
	Words   string
}

// ContractWorking is the working copy of a contract the stage is writing, over Version. Body is the EpicContract in
// protobuf JSON, and Decisions are its decisions. Created and Updated are unix seconds, and an update keeps Created.
type ContractWorking struct {
	Contract  string
	Version   int64
	Body      string
	Decisions []ContractDecision
	Writer    string
	Created   int64
	Updated   int64
}

// ContractRowWrite is one write to a row of a contract, numbered by Seq within Contract. Version is the version it
// belongs to, and 0 for a draft. Action is one of the RowAction constants. Maker is "" when the writing session made the
// write on its own account. At is unix seconds.
type ContractRowWrite struct {
	Contract, Row, Action, Depends, Words, Maker string
	Seq, Version                                 int64
	Quote, MakerReason, Delegation, Session      string
	At                                           int64
}

// ContractRefusal is a propose the store refused, numbered by Seq within Contract. Version is the version the refused
// propose would have made. At is unix seconds.
type ContractRefusal struct {
	Contract     string
	Seq, Version int64
	Field, Text  string
	Session      string
	At           int64
}

// ContractShown is a row of a contract version the stage showed the owner. At is unix seconds.
type ContractShown struct {
	Contract string
	Version  int64
	Row      string
	At       int64
}

// StepRead is a design step a contract's stage read, over Version of the contract. Version is 0 for a draft.
type StepRead struct {
	Step, Contract string
	Version        int64
}
