package guiding

import (
	"image"
	"runtime"
	"sort"
)

const (
	minimumSeriesFramesCount = 2
)

type FrameSeriesRequest struct {
	Frames       []image.Image
	MaxStars     int
	PixelToMotor PixelToMotorMatrix
}

type FrameSeriesPoint struct {
	FrameIndex      int               `json:"frame_index"`
	DeltaX          float64           `json:"delta_x"`
	DeltaY          float64           `json:"delta_y"`
	RotationDeg     float64           `json:"rotation_deg"`
	Confidence      float64           `json:"confidence"`
	MatchedStars    int               `json:"matched_stars"`
	SuggestedMotorX float64           `json:"suggested_motor_x_ms"`
	SuggestedMotorY float64           `json:"suggested_motor_y_ms"`
	Error           string            `json:"error,omitempty"`
	Raw             *FrameShiftResult `json:"raw,omitempty"`
}

type FrameSeriesResult struct {
	ReferenceFrameIndex int                `json:"reference_frame_index"`
	TotalFrames         int                `json:"total_frames"`
	SolvedFrames        int                `json:"solved_frames"`
	FailedFrames        int                `json:"failed_frames"`
	Points              []FrameSeriesPoint `json:"points"`
}

type seriesJob struct {
	frameIndex int
	frame      image.Image
}

type seriesSolveResult struct {
	point FrameSeriesPoint
}

// AnalyzeFrameSeries compares each frame against the first frame and returns a time-series drift report.
// The pipeline uses channels to keep stage ownership explicit and avoid shared mutable state.
func AnalyzeFrameSeries(request FrameSeriesRequest) FrameSeriesResult {
	if len(request.Frames) < minimumSeriesFramesCount {
		return FrameSeriesResult{
			ReferenceFrameIndex: 0,
			TotalFrames:         len(request.Frames),
			SolvedFrames:        0,
			FailedFrames:        len(request.Frames),
			Points: []FrameSeriesPoint{{
				FrameIndex: 0,
				Error:      "at least two frames are required for series analysis",
			}},
		}
	}

	referenceFrame := request.Frames[0]
	requestedWorkerCount := runtime.NumCPU()
	if requestedWorkerCount < 1 {
		requestedWorkerCount = 1
	}
	if requestedWorkerCount > 4 {
		requestedWorkerCount = 4
	}

	jobs := make(chan seriesJob)
	results := make(chan seriesSolveResult)

	for workerIndex := 0; workerIndex < requestedWorkerCount; workerIndex += 1 {
		go runFrameSeriesWorker(referenceFrame, request, jobs, results)
	}

	go enqueueFrameSeriesJobs(request.Frames[1:], jobs)

	collectedPoints := make([]FrameSeriesPoint, 0, len(request.Frames)-1)
	for expectedResultIndex := 0; expectedResultIndex < len(request.Frames)-1; expectedResultIndex += 1 {
		solveResult := <-results
		collectedPoints = append(collectedPoints, solveResult.point)
	}

	sort.Slice(collectedPoints, func(leftIndex int, rightIndex int) bool {
		return collectedPoints[leftIndex].FrameIndex < collectedPoints[rightIndex].FrameIndex
	})

	solvedFramesCount := 0
	failedFramesCount := 0
	for _, point := range collectedPoints {
		if point.Error == "" {
			solvedFramesCount += 1
			continue
		}
		failedFramesCount += 1
	}

	return FrameSeriesResult{
		ReferenceFrameIndex: 0,
		TotalFrames:         len(request.Frames),
		SolvedFrames:        solvedFramesCount,
		FailedFrames:        failedFramesCount,
		Points:              collectedPoints,
	}
}

func enqueueFrameSeriesJobs(frames []image.Image, jobs chan<- seriesJob) {
	for frameOffset, frame := range frames {
		jobs <- seriesJob{frameIndex: frameOffset + 1, frame: frame}
	}
	close(jobs)
}

func runFrameSeriesWorker(referenceFrame image.Image, request FrameSeriesRequest, jobs <-chan seriesJob, results chan<- seriesSolveResult) {
	for job := range jobs {
		solveResult, solveError := AnalyzeFrameShift(FrameShiftRequest{
			ReferenceFrame: referenceFrame,
			CurrentFrame:   job.frame,
			MaxStars:       request.MaxStars,
			PixelToMotor:   request.PixelToMotor,
		})
		if solveError != nil {
			results <- seriesSolveResult{
				point: FrameSeriesPoint{FrameIndex: job.frameIndex, Error: solveError.Error()},
			}
			continue
		}

		results <- seriesSolveResult{
			point: FrameSeriesPoint{
				FrameIndex:      job.frameIndex,
				DeltaX:          solveResult.DeltaX,
				DeltaY:          solveResult.DeltaY,
				RotationDeg:     solveResult.RotationDeg,
				Confidence:      solveResult.Confidence,
				MatchedStars:    solveResult.MatchedStars,
				SuggestedMotorX: solveResult.SuggestedMotor.MotorXMs,
				SuggestedMotorY: solveResult.SuggestedMotor.MotorYMs,
				Raw:             &solveResult,
			},
		}
	}
}
