package ask

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/firoz1860/sutra-mini/backend/internal/store"
)

var (
	ErrNoSKU   = errors.New("could not identify a SKU in the question")
	ErrNoParty = errors.New("could not identify a job-worker in the question")
)

type Service struct {
	db       *store.DB
	logger   *log.Logger
	modelKey string
}

func New(db *store.DB, logger *log.Logger, modelKey string) *Service {
	if logger == nil {
		logger = log.Default()
	}
	return &Service{db: db, logger: logger, modelKey: modelKey}
}

type Response struct {
	Question       string   `json:"question"`
	Answer         string   `json:"answer"`
	SKU            string   `json:"sku"`
	Party          string   `json:"party"`
	OutstandingQty int      `json:"outstanding_qty"`
	ToolCalls      []string `json:"tool_calls"`
	ModelStubbed   bool     `json:"model_stubbed"`
}

func (s *Service) Answer(ctx context.Context, question string) (Response, error) {
	resp := Response{Question: question, ModelStubbed: s.modelKey == "", ToolCalls: []string{}}

	sku, call, err := s.resolveSKU(ctx, question)
	resp.ToolCalls = append(resp.ToolCalls, call)
	if err != nil {
		return resp, err
	}

	party, call, err := s.resolveParty(ctx, question)
	resp.ToolCalls = append(resp.ToolCalls, call)
	if err != nil {
		return resp, err
	}

	outstanding, call, err := s.outstanding(ctx, sku.Code, party.Name)
	resp.ToolCalls = append(resp.ToolCalls, call)
	if err != nil {
		return resp, err
	}

	resp.SKU = sku.Name
	resp.Party = party.Name
	resp.OutstandingQty = outstanding
	resp.Answer = fmt.Sprintf("%d unit(s) of %s (%s) are still with %s.", outstanding, sku.Name, sku.Code, party.Name)
	return resp, nil
}

func (s *Service) resolveSKU(ctx context.Context, question string) (store.SKURef, string, error) {
	skus, err := s.db.ListSKUs(ctx)
	call := fmt.Sprintf("resolve_sku(listed=%d)", len(skus))
	s.logger.Printf("ask tool=%s", call)
	if err != nil {
		return store.SKURef{}, call, err
	}
	q := strings.ToLower(question)
	for _, sku := range skus {
		if strings.Contains(q, strings.ToLower(sku.Code)) || strings.Contains(q, strings.ToLower(sku.Name)) {
			call = fmt.Sprintf("resolve_sku -> %s", sku.Code)
			s.logger.Printf("ask tool=%s", call)
			return sku, call, nil
		}
	}
	return store.SKURef{}, call, ErrNoSKU
}

func (s *Service) resolveParty(ctx context.Context, question string) (store.PartyRef, string, error) {
	parties, err := s.db.ListParties(ctx)
	call := fmt.Sprintf("resolve_party(listed=%d)", len(parties))
	s.logger.Printf("ask tool=%s", call)
	if err != nil {
		return store.PartyRef{}, call, err
	}
	q := strings.ToLower(question)
	for _, p := range parties {
		if strings.Contains(q, strings.ToLower(p.Name)) {
			call = fmt.Sprintf("resolve_party -> %s", p.Name)
			s.logger.Printf("ask tool=%s", call)
			return p, call, nil
		}
	}
	return store.PartyRef{}, call, ErrNoParty
}

func (s *Service) outstanding(ctx context.Context, skuCode, partyName string) (int, string, error) {
	qty, found, err := s.db.OutstandingForSKUParty(ctx, skuCode, partyName)
	call := fmt.Sprintf("get_outstanding(sku=%s, party=%s) -> %d", skuCode, partyName, qty)
	s.logger.Printf("ask tool=%s found=%v", call, found)
	if err != nil {
		return 0, call, err
	}
	return qty, call, nil
}
