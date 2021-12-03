package reactor

type Logger interface {
	Log(keyvals ...interface{}) error
}

type nopLogger struct {
}

func (n *nopLogger) Log(keyvals ...interface{}) error {
	return nil
}
