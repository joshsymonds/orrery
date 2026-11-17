package server

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

func TestEpicArchiveRowsForwardKindAndIDUnchanged(t *testing.T) {
	t.Parallel()
	cases := []epicCommandCase{
		{
			name: "archive",
			call: func(ctx context.Context, client orreryv1connect.EpicsClient) error {
				_, err := client.Archive(ctx, connect.NewRequest(&orreryv1.EpicsArchiveRequest{
					Kind: "headless", Id: "synthetic-headless",
				}))
				return err
			},
			want: &enginev1.ArchiveRowRequest{Kind: "headless", Id: "synthetic-headless"},
		},
		{
			name: "unarchive",
			call: func(ctx context.Context, client orreryv1connect.EpicsClient) error {
				_, err := client.Unarchive(ctx, connect.NewRequest(&orreryv1.EpicsUnarchiveRequest{
					Kind: "epic", Id: syntheticEpicID,
				}))
				return err
			},
			want: &enginev1.UnarchiveRowRequest{Kind: "epic", Id: syntheticEpicID},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &epicCommandsFake{}
			client := epicCommandServe(t, fake)
			if err := tc.call(t.Context(), client); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			assertForwarded(t, fake.recorded(), tc.want)
		})
	}
}

func TestEpicArchiveRowsWithoutCommandClientIsUnavailable(t *testing.T) {
	t.Parallel()
	client := epicCommandServe(t, nil)
	calls := map[string]func() error{
		"archive": func() error {
			_, err := client.Archive(t.Context(), connect.NewRequest(&orreryv1.EpicsArchiveRequest{
				Kind: "epic", Id: syntheticEpicID,
			}))
			return err
		},
		"unarchive": func() error {
			_, err := client.Unarchive(t.Context(), connect.NewRequest(&orreryv1.EpicsUnarchiveRequest{
				Kind: "epic", Id: syntheticEpicID,
			}))
			return err
		},
	}
	for name, call := range calls {
		err := call()
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnavailable ||
			connectErr.Message() != epicCommandsUnavailable {
			t.Fatalf("%s: error = %v, want %v %q", name, err, connect.CodeUnavailable, epicCommandsUnavailable)
		}
	}
}
