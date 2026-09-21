package email

type Sender interface {
	Send([]string, string, string) error
}
