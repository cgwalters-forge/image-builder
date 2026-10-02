package bootc

func (cnt *Container) ID() string {
	return cnt.id
}

var ComposefsBackend = composefsBackend
