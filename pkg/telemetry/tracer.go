package telemetry

import "context"

type Tracer struct{}

func NewTracer() *Tracer {
	return &Tracer{}
}

func (t *Tracer) StartSpan(ctx context.Context, name string) (context.Context, func()) {
	return ctx, func() {}
}

func (t *Tracer) Inject(ctx context.Context, carrier map[string]string) {
}

func (t *Tracer) Extract(ctx context.Context, carrier map[string]string) context.Context {
	return ctx
}
