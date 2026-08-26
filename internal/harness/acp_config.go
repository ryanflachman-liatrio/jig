package harness

import (
	"context"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
)

// acpSessionConfigPolicy lets an ACP backend opt into only the session
// configuration selectors it has verified. New ACP harnesses can reuse the
// semantic categories without relying on Codex's option IDs.
type acpSessionConfigPolicy interface {
	Apply(context.Context, *acp.Conn, string, SessionSpec) error
}

type semanticACPConfigPolicy struct {
	model  bool
	effort bool
}

func (p semanticACPConfigPolicy) Apply(ctx context.Context, conn *acp.Conn, sessionID string, spec SessionSpec) error {
	if p.model {
		if err := conn.SetSelectConfigByCategory(ctx, sessionID, acpsdk.SessionConfigOptionCategoryModel, spec.Model); err != nil {
			return err
		}
	}
	if p.effort {
		if err := conn.SetSelectConfigByCategory(ctx, sessionID, acpsdk.SessionConfigOptionCategoryThoughtLevel, spec.Effort); err != nil {
			return err
		}
	}
	return nil
}
