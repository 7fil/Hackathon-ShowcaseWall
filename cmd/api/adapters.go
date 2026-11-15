package main

import (
	"context"
	"errors"

	mediasvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/service"
	workssvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/service"
)

type worksCoverLookup struct {
	works *workssvc.Service
}

var _ mediasvc.WorkLookup = worksCoverLookup{}

func (l worksCoverLookup) LookupCover(ctx context.Context, workID string) (*mediasvc.WorkCover, error) {
	info, err := l.works.CoverInfo(ctx, workID)
	if errors.Is(err, workssvc.ErrNotFound) {
		return nil, mediasvc.ErrWorkNotFound
	}
	if err != nil {
		return nil, err
	}
	return &mediasvc.WorkCover{
		CoverKey:    info.CoverKey,
		ContentType: info.ContentType,
		SizeBytes:   info.SizeBytes,
		Published:   info.Published,
	}, nil
}

type worksScreenshotLookup struct {
	works *workssvc.Service
}

var _ mediasvc.ScreenshotLookup = worksScreenshotLookup{}

func (l worksScreenshotLookup) LookupScreenshot(ctx context.Context, workID, imageID string) (*mediasvc.WorkCover, error) {
	info, err := l.works.ScreenshotInfo(ctx, workID, imageID)
	if errors.Is(err, workssvc.ErrNotFound) {
		return nil, mediasvc.ErrWorkNotFound
	}
	if err != nil {
		return nil, err
	}
	return &mediasvc.WorkCover{
		CoverKey:    info.CoverKey,
		ContentType: info.ContentType,
		SizeBytes:   info.SizeBytes,
		Published:   info.Published,
	}, nil
}
