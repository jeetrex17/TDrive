package auth

import (
	"context"
	"fmt"
	"sync"

	coreauth "TDrive/backend/auth"
)

type EventSink interface {
	Emit(name string, args ...any)
}

type Service struct {
	Events EventSink

	codech chan string
	passch chan string
	mu     sync.Mutex
	stage  loginStage
}

type loginStage int

const (
	stageIdle loginStage = iota
	stageStarted
	stageWaitingCode
	stageWaitingPassword
)

func NewService(events EventSink) *Service {
	return &Service{
		Events: events,
		codech: make(chan string, 1),
		passch: make(chan string, 1),
	}
}

func (s *Service) StartLogin(ctx context.Context, phoneNumber string) error {
	s.resetAttempt(stageStarted)
	client, err := coreauth.Connect()
	if err != nil {
		fmt.Println("Could not connect to Telegram:", err)
		s.finishAttempt()
		s.emit("login-error", err.Error())
		return err
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.finishAttempt()
				s.emit("login-error", fmt.Sprintf("login panic: %v", r))
			}
		}()
		err := coreauth.StartLogin(ctx, client, s, phoneNumber)
		if err != nil {
			fmt.Println("Login failed:", err)
			s.finishAttempt()
			s.emit("login-error", err.Error())
			return
		}

		fmt.Println("Login Flow Complete. Emitting Success Event.")
		s.finishAttempt()
		s.emit("login-success", true)
	}()
	return nil
}

func (s *Service) IsLoggedIn(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	login, err := coreauth.CheckLogin(ctx)
	if err != nil {
		fmt.Println("error auto login", err)
		return false
	}
	return login
}

func (s *Service) SystemStatus() string {
	_, err := coreauth.LoadImpCredentials()
	if err != nil {
		return "NEEDS_SETUP"
	}
	return "READY_FOR_LOGIN"
}

func (s *Service) SaveSetup(apiID int, apiHash string) string {
	if err := coreauth.SaveImpCredentials(apiID, apiHash); err != nil {
		return "Error: " + err.Error()
	}
	return "Success"
}

func (s *Service) SubmitCode(code string) {
	if !s.accepts(stageStarted, stageWaitingCode) {
		s.emit("login-error", "Not waiting for a login code. Start login again.")
		return
	}
	sendLatest(s.codech, code)
}

func (s *Service) SubmitPassword(password string) {
	if !s.accepts(stageWaitingPassword) {
		s.emit("login-error", "Not waiting for a password.")
		return
	}
	sendLatest(s.passch, password)
}

func (s *Service) SendHint(hint string) {
	s.emit("gothint", hint)
}

func (s *Service) WaitCode(ctx context.Context) (string, error) {
	s.setStage(stageWaitingCode)
	select {
	case code := <-s.codech:
		return code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *Service) WaitPassword(ctx context.Context, hint string) (string, error) {
	s.setStage(stageWaitingPassword)
	s.emit("login-password-required", true)
	s.SendHint(hint)
	select {
	case password := <-s.passch:
		return password, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *Service) Codech() chan string {
	return s.codech
}

func (s *Service) Passch() chan string {
	return s.passch
}

func (s *Service) GetCodech() chan string {
	return s.Codech()
}

func (s *Service) GetPassch() chan string {
	return s.Passch()
}

func (s *Service) emit(name string, args ...any) {
	if s.Events != nil {
		s.Events.Emit(name, args...)
	}
}

func (s *Service) resetAttempt(stage loginStage) {
	s.mu.Lock()
	s.stage = stage
	drain(s.codech)
	drain(s.passch)
	s.mu.Unlock()
}

func (s *Service) finishAttempt() {
	s.mu.Lock()
	s.stage = stageIdle
	drain(s.codech)
	drain(s.passch)
	s.mu.Unlock()
}

func (s *Service) setStage(stage loginStage) {
	s.mu.Lock()
	s.stage = stage
	s.mu.Unlock()
}

func (s *Service) accepts(stages ...loginStage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stage := range stages {
		if s.stage == stage {
			return true
		}
	}
	return false
}

func sendLatest(ch chan string, value string) {
	select {
	case ch <- value:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- value:
	default:
	}
}

func drain(ch chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
