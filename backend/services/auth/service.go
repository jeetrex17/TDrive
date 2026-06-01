package auth

import (
	"context"
	"fmt"

	coreauth "TDrive/backend/auth"
)

type EventSink interface {
	Emit(name string, args ...any)
}

type Service struct {
	Events EventSink

	codech chan string
	passch chan string
}

func NewService(events EventSink) *Service {
	return &Service{
		Events: events,
		codech: make(chan string),
		passch: make(chan string),
	}
}

func (s *Service) StartLogin(ctx context.Context, phoneNumber string) {
	client, err := coreauth.Connect()
	if err != nil {
		fmt.Println("Could not connect to Telegram:", err)
		return
	}

	go func() {
		err := coreauth.StartLogin(ctx, client, s, phoneNumber)
		if err != nil {
			fmt.Println("Login failed:", err)
			return
		}

		fmt.Println("Login Flow Complete. Emitting Success Event.")
		s.emit("login-success", true)
	}()
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
	s.codech <- code
}

func (s *Service) SubmitPassword(password string) {
	s.passch <- password
}

func (s *Service) SendHint(hint string) {
	s.emit("gothint", hint)
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
