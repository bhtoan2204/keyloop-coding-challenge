package projection

import (
	"context"
	"fmt"
	"keyloop-coding-challenge/internal/infrastructure/projection/projectors"
	"keyloop-coding-challenge/package/eventsourcing"

	"golang.org/x/sync/errgroup"
)

type Projection interface {
	Apply(ctx context.Context, ev eventsourcing.Event) error
}

type projectionPipeline struct {
	stages [][]projectors.Projector
}

func NewProjection(projs ...projectors.Projector) Projection {
	return &projectionPipeline{
		stages: [][]projectors.Projector{
			projs,
		},
	}
}

func (p *projectionPipeline) Apply(ctx context.Context, ev eventsourcing.Event) error {
	for stageIdx, stage := range p.stages {
		if err := p.runStage(ctx, stage, ev); err != nil {
			return fmt.Errorf("projection failed at stage %d for event %T: %w", stageIdx, ev, err)
		}
	}
	return nil
}

func (p *projectionPipeline) runStage(ctx context.Context, stage []projectors.Projector, ev eventsourcing.Event) error {
	if len(stage) == 1 {
		return stage[0].Handle(ctx, ev)
	}

	g, groupCtx := errgroup.WithContext(ctx)

	for _, proj := range stage {
		g.Go(func() error {
			if err := proj.Handle(groupCtx, ev); err != nil {
				return fmt.Errorf("projector %T handle error: %w", proj, err)
			}
			return nil
		})
	}

	return g.Wait()
}
