package service

type Service struct {
	Name    string
	Path    string
	Content string
}

func NewService(name, path, content string) *Service {
	return &Service{
		Name:    name,
		Path:    path,
		Content: content,
	}
}
