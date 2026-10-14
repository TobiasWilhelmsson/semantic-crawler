package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"semantic-crawler/internal/semantic"
)

func serverModelResolver(_ semantic.SourceEntry, mode string) (semantic.SemanticModel, string, error) {
	model, err := semanticModel(mode)
	if err != nil {
		return nil, "", fmt.Errorf("configured model is unavailable; set SEMANTIC_LLM_PROVIDER (openai, gemini, deepseek, qwen, zai, or custom), SEMANTIC_LLM_MODEL and SEMANTIC_LLM_API_KEY in .env; custom providers also need SEMANTIC_LLM_BASE_URL; check timeout/retry settings and the Quick Start guide")
	}
	return model, modelName(mode), nil
}
func openReviewWorkspace(ctx context.Context, source semantic.SourceEntry, path string, create bool, models semantic.ModelResolver, allowlist []string, legacyMode string) (*semantic.SourceWorkspace, error) {
	store, err := semantic.OpenReviewStore(ctx, path, create)
	if err != nil {
		return nil, err
	}
	workspace := &semantic.SourceWorkspace{Source: source, DBPath: path, Store: store, Reviews: semantic.NewReviewService(store), Deliveries: semantic.NewRestrictedDeliveryService(store, semantic.NewHTTPDeliveryClient(nil), allowlist).WithDocumentSource(source)}
	workspace.Retrier = semantic.NewWorkspaceRetrier(workspace, models, legacyMode)
	return workspace, nil
}
func serveReview(ctx context.Context, address string, handler http.Handler, shutdown func(context.Context) error, ready ...func() error) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	listener, listenErr := net.Listen("tcp", address)
	if listenErr == nil {
		listenErr = ctx.Err()
		for _, start := range ready {
			if listenErr == nil && start != nil {
				listenErr = start()
			}
		}
		if listenErr != nil {
			listener.Close()
		} else {
			go func() { done <- server.Serve(listener) }()
			select {
			case listenErr = <-done:
			case <-ctx.Done():
			}
		}
	}
	grace, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	httpDone := make(chan error, 1)
	go func() { httpDone <- server.Shutdown(grace) }()
	workerErr := shutdown(grace)
	httpErr := <-httpDone
	if errors.Is(listenErr, http.ErrServerClosed) {
		listenErr = nil
	}
	return errors.Join(listenErr, workerErr, httpErr)
}
