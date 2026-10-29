package commands

import (
	"context"

	"keyloop-coding-challenge/internal/application/command"
)

// Service is the write side used by the command endpoints (*command.Service).
type Service interface {
	StockVehicle(ctx context.Context, cmd command.StockVehicle, meta command.Meta) (*command.Result, error)
	ProposeAction(ctx context.Context, cmd command.ProposeAction, meta command.Meta) (*command.Result, error)
	ReviseAction(ctx context.Context, cmd command.ReviseAction, meta command.Meta) (*command.Result, error)
	CancelAction(ctx context.Context, cmd command.CancelAction, meta command.Meta) (*command.Result, error)
	SellVehicle(ctx context.Context, cmd command.SellVehicle, meta command.Meta) (*command.Result, error)
}
