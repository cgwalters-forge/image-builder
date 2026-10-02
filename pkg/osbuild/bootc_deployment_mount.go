package osbuild

// BootcDeploymentMountOptions are the options of the
// org.osbuild.bootc.deployment mount, which mounts the deployment of a
// bootc installation of either backend (ostree or composefs) with
// "bootc install mount" from the build root.
type BootcDeploymentMountOptions struct {
	// Where the physical root of the installation is, like for the
	// ostree deployment mount
	Source OSTreeMountSource `json:"source,omitempty"`
}

func (BootcDeploymentMountOptions) isMountOptions() {}

func NewBootcDeploymentMount(name string, source OSTreeMountSource) *Mount {
	return &Mount{
		Type: "org.osbuild.bootc.deployment",
		Name: name,
		Options: &BootcDeploymentMountOptions{
			Source: source,
		},
	}
}
