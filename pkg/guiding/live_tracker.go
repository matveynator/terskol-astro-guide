package guiding

import (
	"errors"
	"image"
)

type LiveTracker struct {
	commands chan liveTrackerCommand
}

type LiveTrackerSnapshot struct {
	SessionActive    bool                   `json:"session_active"`
	ReferenceWidth   int                    `json:"reference_width"`
	ReferenceHeight  int                    `json:"reference_height"`
	ProcessedFrames  int                    `json:"processed_frames"`
	SuccessfulFrames int                    `json:"successful_frames"`
	FailedFrames     int                    `json:"failed_frames"`
	LastResult       FrameSeriesPoint       `json:"last_result"`
	OperatorHint     ManualCorrectionAdvice `json:"operator_hint"`
}

type LiveTrackerSessionConfig struct {
	ReferenceFrame image.Image
	MaxStars       int
	PixelToMotor   PixelToMotorMatrix
}

type liveTrackerCommand struct {
	kind   string
	config LiveTrackerSessionConfig
	frame  image.Image
	reply  chan liveTrackerReply
}

type liveTrackerReply struct {
	snapshot LiveTrackerSnapshot
	err      error
}

type liveTrackerState struct {
	sessionActive    bool
	referenceFrame   image.Image
	maxStars         int
	pixelToMotor     PixelToMotorMatrix
	processedFrames  int
	successfulFrames int
	failedFrames     int
	lastResult       FrameSeriesPoint
	operatorHint     ManualCorrectionAdvice
}

// StartLiveTracker creates a goroutine-owned session tracker for step-3 live frame analysis.
// The command channel is the single synchronization point to avoid shared mutable state.
func StartLiveTracker() *LiveTracker {
	tracker := &LiveTracker{commands: make(chan liveTrackerCommand)}
	go runLiveTrackerLoop(tracker.commands)
	return tracker
}

func (tracker *LiveTracker) StartSession(config LiveTrackerSessionConfig) (LiveTrackerSnapshot, error) {
	reply := make(chan liveTrackerReply, 1)
	tracker.commands <- liveTrackerCommand{kind: "start", config: config, reply: reply}
	response := <-reply
	return response.snapshot, response.err
}

func (tracker *LiveTracker) AnalyzeFrame(frame image.Image) (LiveTrackerSnapshot, error) {
	reply := make(chan liveTrackerReply, 1)
	tracker.commands <- liveTrackerCommand{kind: "frame", frame: frame, reply: reply}
	response := <-reply
	return response.snapshot, response.err
}

func (tracker *LiveTracker) Snapshot() LiveTrackerSnapshot {
	reply := make(chan liveTrackerReply, 1)
	tracker.commands <- liveTrackerCommand{kind: "snapshot", reply: reply}
	response := <-reply
	return response.snapshot
}

func runLiveTrackerLoop(commands <-chan liveTrackerCommand) {
	state := liveTrackerState{}
	for command := range commands {
		switch command.kind {
		case "start":
			nextState, startError := startLiveTrackerSession(state, command.config)
			if startError == nil {
				state = nextState
			}
			command.reply <- liveTrackerReply{snapshot: buildLiveTrackerSnapshot(state), err: startError}
		case "frame":
			nextState, frameError := analyzeLiveTrackerFrame(state, command.frame)
			if frameError == nil || nextState.sessionActive {
				state = nextState
			}
			command.reply <- liveTrackerReply{snapshot: buildLiveTrackerSnapshot(state), err: frameError}
		case "snapshot":
			command.reply <- liveTrackerReply{snapshot: buildLiveTrackerSnapshot(state), err: nil}
		default:
			command.reply <- liveTrackerReply{snapshot: buildLiveTrackerSnapshot(state), err: errors.New("unknown tracker command")}
		}
	}
}

func startLiveTrackerSession(currentState liveTrackerState, config LiveTrackerSessionConfig) (liveTrackerState, error) {
	if config.ReferenceFrame == nil {
		return currentState, errors.New("reference frame is required")
	}
	referenceBounds := config.ReferenceFrame.Bounds()
	if referenceBounds.Dx() < 3 || referenceBounds.Dy() < 3 {
		return currentState, errors.New("reference frame is too small")
	}

	return liveTrackerState{
		sessionActive:    true,
		referenceFrame:   config.ReferenceFrame,
		maxStars:         config.MaxStars,
		pixelToMotor:     config.PixelToMotor,
		processedFrames:  0,
		successfulFrames: 0,
		failedFrames:     0,
		lastResult:       FrameSeriesPoint{},
		operatorHint:     ManualCorrectionAdvice{},
	}, nil
}

func analyzeLiveTrackerFrame(currentState liveTrackerState, frame image.Image) (liveTrackerState, error) {
	if !currentState.sessionActive {
		return currentState, errors.New("live tracker session is not started")
	}

	nextState := currentState
	nextState.processedFrames += 1
	currentFrameIndex := nextState.processedFrames

	shiftResult, shiftError := AnalyzeFrameShift(FrameShiftRequest{
		ReferenceFrame: currentState.referenceFrame,
		CurrentFrame:   frame,
		MaxStars:       currentState.maxStars,
		PixelToMotor:   currentState.pixelToMotor,
	})
	if shiftError != nil {
		nextState.failedFrames += 1
		nextState.lastResult = FrameSeriesPoint{FrameIndex: currentFrameIndex, Error: shiftError.Error()}
		nextState.operatorHint = ManualCorrectionAdvice{
			ShouldAct: false,
			Summary:   "Frame solve failed. Wait for the next frame before manual correction.",
		}
		return nextState, shiftError
	}

	nextState.successfulFrames += 1
	nextState.lastResult = FrameSeriesPoint{
		FrameIndex:      currentFrameIndex,
		DeltaX:          shiftResult.DeltaX,
		DeltaY:          shiftResult.DeltaY,
		RotationDeg:     shiftResult.RotationDeg,
		Confidence:      shiftResult.Confidence,
		MatchedStars:    shiftResult.MatchedStars,
		SuggestedMotorX: shiftResult.SuggestedMotor.MotorXMs,
		SuggestedMotorY: shiftResult.SuggestedMotor.MotorYMs,
		Raw:             &shiftResult,
	}
	nextState.operatorHint = BuildManualCorrectionAdvice(
		shiftResult.DeltaX,
		shiftResult.DeltaY,
		shiftResult.SuggestedMotor.MotorXMs,
		shiftResult.SuggestedMotor.MotorYMs,
	)
	return nextState, nil
}

func buildLiveTrackerSnapshot(state liveTrackerState) LiveTrackerSnapshot {
	referenceWidth := 0
	referenceHeight := 0
	if state.referenceFrame != nil {
		referenceWidth = state.referenceFrame.Bounds().Dx()
		referenceHeight = state.referenceFrame.Bounds().Dy()
	}

	return LiveTrackerSnapshot{
		SessionActive:    state.sessionActive,
		ReferenceWidth:   referenceWidth,
		ReferenceHeight:  referenceHeight,
		ProcessedFrames:  state.processedFrames,
		SuccessfulFrames: state.successfulFrames,
		FailedFrames:     state.failedFrames,
		LastResult:       state.lastResult,
		OperatorHint:     state.operatorHint,
	}
}
