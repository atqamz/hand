package board

type consoleView struct {
	supervisorView
	Controls bool
	Base     string
	Token    string
	Session  string
}

func buildConsole(sv supervisorView, c viewContext) consoleView {
	return consoleView{
		supervisorView: sv,
		Controls:       c.Controls,
		Base:           c.Base,
		Token:          c.Token,
		Session:        c.Session,
	}
}
