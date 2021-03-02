package service

// ServiceInjectionManifestFile service injection manifest file structure
type ServiceInjectionManifestFile struct {
	Name    string
	Dropins []struct {
		Contents string
	}
}

// ServiceManifestFile service manifest file structure
type ServiceManifestFile struct {
	Name     string
	Contents string
}

// ScriptManifestFile script manifest file structure
type ScriptManifestFile struct {
	Path     string
	Contents struct {
		Inline string
	}
}
