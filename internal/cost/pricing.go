package cost

import (
	"fmt"

	"agent/internal/model"
)

type PriceBook struct {
	prices map[string]Pricing
}

func NewPriceBookFromRegistry(registry *model.Registry) PriceBook {
	book := PriceBook{prices: make(map[string]Pricing)}
	if registry == nil {
		return book
	}
	for _, metadata := range registry.Models() {
		book.prices[metadata.ID] = Pricing{
			InputPer1M:  metadata.Pricing.InputPer1M,
			OutputPer1M: metadata.Pricing.OutputPer1M,
		}
	}
	return book
}

func (b PriceBook) Pricing(modelID string) (Pricing, error) {
	pricing, ok := b.prices[modelID]
	if !ok {
		return Pricing{}, fmt.Errorf("pricing not found for model %q", modelID)
	}
	return pricing, nil
}

type Estimator struct {
	Prices PriceBook
}

func EstimateCost(tokens int, pricePer1M float64) float64 {
	return float64(tokens) * pricePer1M / 1_000_000
}

func (e Estimator) Estimate(record Usage) (Usage, error) {
	pricing, err := e.Prices.Pricing(record.ModelID)
	if err != nil {
		return Usage{}, err
	}
	record.EstimatedCostUSD = EstimateCost(record.InputTokens, pricing.InputPer1M) +
		EstimateCost(record.OutputTokens, pricing.OutputPer1M)
	record.BillableUnits = float64(record.InputTokens + record.OutputTokens)
	return record, nil
}
