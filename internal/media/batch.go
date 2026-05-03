package media

import (
	"context"
	"errors"
	"sync"
)

type batchItem struct {
	key   string
	index int
	att   MediaAttachment
}

func (o *Orchestrator) processBatch(ctx context.Context, req MediaUnderstandingRequest) ([]MediaOutput, error) {
	if len(req.Attachments) == 0 {
		return nil, nil
	}
	prompt := normalizePrompt(req.Prompt)
	mode := normalizeUnderstandingMode(req.Mode)
	outputs := make([]MediaOutput, 0, len(req.Attachments))
	slots := make(map[int]MediaOutput)
	groups := map[string][]int{}
	work := map[string]batchItem{}
	for i, att := range req.Attachments {
		if !(att.IsImage() || att.IsAudio() || att.IsVideo()) {
			continue
		}
		key := BuildCacheKey(att, prompt, mode)
		if cached, ok := o.cache.Get(key); ok {
			cached.AttachmentIndex = i
			slots[i] = cached
			continue
		}
		groups[key] = append(groups[key], i)
		if _, ok := work[key]; !ok {
			work[key] = batchItem{key: key, index: i, att: att}
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	workers := o.maxConcurrent
	if workers <= 0 {
		workers = 1
	}
	if workers > len(work) && len(work) > 0 {
		workers = len(work)
	}
	jobs := make(chan batchItem)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	worker := func() {
		defer wg.Done()
		for it := range jobs {
			select {
			case <-ctx.Done():
				return
			default:
			}
			out, err := o.processOne(ctx, it.index, it.att, prompt, mode)
			if errors.Is(err, errUnsupported) {
				continue
			}
			mu.Lock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				continue
			}
			o.cache.Set(it.key, out)
			for _, idx := range groups[it.key] {
				cp := out
				cp.AttachmentIndex = idx
				slots[idx] = cp
			}
			mu.Unlock()
		}
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}
sendLoop:
	for _, item := range work {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	for i := range req.Attachments {
		if out, ok := slots[i]; ok {
			outputs = append(outputs, out)
		}
	}
	return outputs, nil
}
