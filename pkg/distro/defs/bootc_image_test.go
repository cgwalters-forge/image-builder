package defs

import (
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/osbuild/blueprint/pkg/blueprint"
	"github.com/osbuild/image-builder/internal/common"
	"github.com/osbuild/image-builder/pkg/customizations/subscription"
	"github.com/osbuild/image-builder/pkg/datasizes"
	"github.com/osbuild/image-builder/pkg/disk"
	"github.com/osbuild/image-builder/pkg/disk/partition"
	"github.com/osbuild/image-builder/pkg/distro"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createRand() *rand.Rand {
	return rand.New(rand.NewSource(0))
}

func TestCheckFilesystemCustomizationsValidates(t *testing.T) {
	for _, tc := range []struct {
		fsCust      []blueprint.FilesystemCustomization
		ptmode      partition.PartitioningMode
		expectedErr string
	}{
		// happy
		{
			fsCust:      []blueprint.FilesystemCustomization{},
			expectedErr: "",
		},
		{
			fsCust:      []blueprint.FilesystemCustomization{},
			ptmode:      partition.BtrfsPartitioningMode,
			expectedErr: "",
		},
		{
			fsCust: []blueprint.FilesystemCustomization{
				{Mountpoint: "/"}, {Mountpoint: "/boot"},
			},
			ptmode:      partition.RawPartitioningMode,
			expectedErr: "",
		},
		{
			fsCust: []blueprint.FilesystemCustomization{
				{Mountpoint: "/"}, {Mountpoint: "/boot"},
			},
			ptmode:      partition.BtrfsPartitioningMode,
			expectedErr: "",
		},
		{
			fsCust: []blueprint.FilesystemCustomization{
				{Mountpoint: "/"},
				{Mountpoint: "/boot"},
				{Mountpoint: "/var/log"},
				{Mountpoint: "/var/data"},
			},
			expectedErr: "",
		},
		// sad
		{
			fsCust: []blueprint.FilesystemCustomization{
				{Mountpoint: "/"},
				{Mountpoint: "/ostree"},
			},
			ptmode:      partition.RawPartitioningMode,
			expectedErr: "the following errors occurred while validating custom mountpoints:\npath \"/ostree\" is not allowed",
		},
		{
			fsCust: []blueprint.FilesystemCustomization{
				{Mountpoint: "/"},
				{Mountpoint: "/var"},
			},
			ptmode:      partition.RawPartitioningMode,
			expectedErr: "the following errors occurred while validating custom mountpoints:\npath \"/var\" is not allowed",
		},
		{
			fsCust: []blueprint.FilesystemCustomization{
				{Mountpoint: "/"},
				{Mountpoint: "/var/data"},
			},
			ptmode:      partition.BtrfsPartitioningMode,
			expectedErr: "the following errors occurred while validating custom mountpoints:\npath \"/var/data\" is not allowed",
		},
		{
			fsCust: []blueprint.FilesystemCustomization{
				{Mountpoint: "/"},
				{Mountpoint: "/boot/"},
			},
			ptmode:      partition.BtrfsPartitioningMode,
			expectedErr: "the following errors occurred while validating custom mountpoints:\npath \"/boot/\" must be canonical",
		},
		{
			fsCust: []blueprint.FilesystemCustomization{
				{Mountpoint: "/"},
				{Mountpoint: "/boot/"},
				{Mountpoint: "/opt"},
			},
			ptmode:      partition.BtrfsPartitioningMode,
			expectedErr: "the following errors occurred while validating custom mountpoints:\npath \"/boot/\" must be canonical\npath \"/opt\" is not allowed",
		},
	} {
		if tc.expectedErr == "" {
			assert.NoError(t, checkFilesystemCustomizations(tc.fsCust, tc.ptmode))
		} else {
			assert.ErrorContains(t, checkFilesystemCustomizations(tc.fsCust, tc.ptmode), tc.expectedErr)
		}
	}
}

func TestLocalMountpointPolicy(t *testing.T) {
	// extended testing of the general mountpoint policy (non-minimal)
	type testCase struct {
		path    string
		allowed bool
	}

	testCases := []testCase{
		// existing mountpoints / and /boot are fine for sizing
		{"/", true},
		{"/boot", true},

		// root mountpoints are not allowed
		{"/data", false},
		{"/opt", false},
		{"/stuff", false},
		{"/usr", false},

		// /var explicitly is not allowed
		{"/var", false},

		// subdirs of /boot are not allowed
		{"/boot/stuff", false},
		{"/boot/loader", false},

		// /var subdirectories are allowed
		{"/var/data", true},
		{"/var/scratch", true},
		{"/var/log", true},
		{"/var/opt", true},
		{"/var/opt/application", true},

		// but not these
		{"/var/home", false},
		{"/var/lock", false}, // symlink to ../run/lock which is on tmpfs
		{"/var/mail", false}, // symlink to spool/mail
		{"/var/mnt", false},
		{"/var/roothome", false},
		{"/var/run", false}, // symlink to ../run which is on tmpfs
		{"/var/srv", false},
		{"/var/usrlocal", false},

		// nor their subdirs
		{"/var/run/subrun", false},
		{"/var/srv/test", false},
		{"/var/home/user", false},
		{"/var/usrlocal/bin", false},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			err := checkFilesystemCustomizations([]blueprint.FilesystemCustomization{{Mountpoint: tc.path}}, partition.RawPartitioningMode)
			if err != nil && tc.allowed {
				t.Errorf("expected %s to be allowed, but got error: %v", tc.path, err)
			} else if err == nil && !tc.allowed {
				t.Errorf("expected %s to be denied, but got no error", tc.path)
			}
		})
	}
}

func TestUpdateFilesystemSizes(t *testing.T) {
	type testCase struct {
		customizations []blueprint.FilesystemCustomization
		minRootSize    uint64
		expected       []blueprint.FilesystemCustomization
	}

	testCases := map[string]testCase{
		"simple": {
			customizations: nil,
			minRootSize:    999,
			expected: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/",
					MinSize:    999,
				},
			},
		},
		"container-is-larger": {
			customizations: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/",
					MinSize:    10,
				},
			},
			minRootSize: 999,
			expected: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/",
					MinSize:    999,
				},
			},
		},
		"container-is-smaller": {
			customizations: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/",
					MinSize:    1000,
				},
			},
			minRootSize: 892,
			expected: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/",
					MinSize:    1000,
				},
			},
		},
		"customizations-noroot": {
			customizations: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/var/data",
					MinSize:    1_000_000,
				},
			},
			minRootSize: 9000,
			expected: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/var/data",
					MinSize:    1_000_000,
				},
				{
					Mountpoint: "/",
					MinSize:    9000,
				},
			},
		},
		"customizations-withroot-smallcontainer": {
			customizations: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/var/data",
					MinSize:    1_000_000,
				},
				{
					Mountpoint: "/",
					MinSize:    2_000_000,
				},
			},
			minRootSize: 9000,
			expected: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/var/data",
					MinSize:    1_000_000,
				},
				{
					Mountpoint: "/",
					MinSize:    2_000_000,
				},
			},
		},
		"customizations-withroot-largecontainer": {
			customizations: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/var/data",
					MinSize:    1_000_000,
				},
				{
					Mountpoint: "/",
					MinSize:    2_000_000,
				},
			},
			minRootSize: 9_000_000,
			expected: []blueprint.FilesystemCustomization{
				{
					Mountpoint: "/var/data",
					MinSize:    1_000_000,
				},
				{
					Mountpoint: "/",
					MinSize:    9_000_000,
				},
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.ElementsMatch(t, updateFilesystemSizes(tc.customizations, tc.minRootSize), tc.expected)
		})
	}

}

func findMountableSizeableFor(pt *disk.PartitionTable, needle string) (disk.Mountable, disk.Sizeable) {
	var foundMnt disk.Mountable
	var foundParent disk.Sizeable
	err := pt.ForEachMountable(func(mnt disk.Mountable, path []disk.Entity) error {
		if mnt.GetMountpoint() == needle {
			foundMnt = mnt
			for idx := len(path) - 1; idx >= 0; idx-- {
				if sz, ok := path[idx].(disk.Sizeable); ok {
					foundParent = sz
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	return foundMnt, foundParent
}

func TestGenPartitionTableSetsRootfsForAllFilesystemsXFS(t *testing.T) {
	rng := createRand()

	imgType := NewTestBootcImageType(t, "qcow2")

	cus := &blueprint.Customizations{
		Filesystem: []blueprint.FilesystemCustomization{
			{Mountpoint: "/var/data", MinSize: 2_000_000},
			{Mountpoint: "/var/stuff", MinSize: 10_000_000},
		},
	}
	rootfsMinSize := uint64(0)
	pt, err := imgType.genPartitionTable(cus, rootfsMinSize, rng)
	assert.NoError(t, err)

	for _, mntPoint := range []string{"/", "/boot", "/var/data"} {
		mnt, _ := findMountableSizeableFor(pt, mntPoint)
		assert.Equal(t, "xfs", mnt.GetFSType())
	}
	_, parent := findMountableSizeableFor(pt, "/var/data")
	assert.True(t, parent.GetSize() >= 2_000_000)

	_, parent = findMountableSizeableFor(pt, "/var/stuff")
	assert.True(t, parent.GetSize() >= 10_000_000)

	// ESP is always vfat
	mnt, _ := findMountableSizeableFor(pt, "/boot/efi")
	assert.Equal(t, "vfat", mnt.GetFSType())
}

func TestGenPartitionTableSetsRootfsForAllFilesystemsBtrfs(t *testing.T) {
	rng := createRand()

	d := NewTestBootcDistro(t)
	d.defaultFs = "btrfs"
	it, err := common.Must(d.GetArch("x86_64")).GetImageType("qcow2")
	assert.NoError(t, err)
	imgType := it.(*bootcImageType)
	cus := &blueprint.Customizations{}
	rootfsMinSize := uint64(0)
	pt, err := imgType.genPartitionTable(cus, rootfsMinSize, rng)
	assert.NoError(t, err)

	mnt, _ := findMountableSizeableFor(pt, "/")
	assert.Equal(t, "btrfs", mnt.GetFSType())

	// btrfs has a default (xfs) /boot
	mnt, _ = findMountableSizeableFor(pt, "/boot")
	assert.Equal(t, "xfs", mnt.GetFSType())

	// ESP is always vfat
	mnt, _ = findMountableSizeableFor(pt, "/boot/efi")
	assert.Equal(t, "vfat", mnt.GetFSType())
}

// the ESP size of the base partition table (501 MiB in the bootc-generic
// definitions) must survive a disk customization that doesn't mention
// /boot/efi
func TestGenPartitionTableDiskCustomizationKeepsESPSize(t *testing.T) {
	rng := createRand()

	imgType := NewTestBootcImageType(t, "qcow2")

	cus := &blueprint.Customizations{
		Disk: &blueprint.DiskCustomization{
			Partitions: []blueprint.PartitionCustomization{
				{
					Type: "plain",
					FilesystemTypedCustomization: blueprint.FilesystemTypedCustomization{
						Mountpoint: "/",
						FSType:     "xfs",
					},
				},
			},
		},
	}
	pt, err := imgType.genPartitionTable(cus, 0, rng)
	assert.NoError(t, err)
	assert.Equal(t, datasizes.Size(501*datasizes.MiB), pt.ESPSize())
}

func TestGenPartitionTableDiskCustomizationRunsValidateLayoutConstraints(t *testing.T) {
	rng := createRand()

	imgType := NewTestBootcImageType(t, "qcow2")

	cus := &blueprint.Customizations{
		Disk: &blueprint.DiskCustomization{
			Partitions: []blueprint.PartitionCustomization{
				{
					Type:            "lvm",
					VGCustomization: blueprint.VGCustomization{},
				},
				{
					Type:            "lvm",
					VGCustomization: blueprint.VGCustomization{},
				},
			},
		},
	}
	_, err := imgType.genPartitionTable(cus, 0, rng)
	assert.EqualError(t, err, "cannot use disk customization: multiple LVM volume groups are not yet supported")
}

func TestGenPartitionTableDiskCustomizationUnknownTypesError(t *testing.T) {
	cus := &blueprint.Customizations{
		Disk: &blueprint.DiskCustomization{
			Partitions: []blueprint.PartitionCustomization{
				{
					Type: "rando",
				},
			},
		},
	}
	_, err := calcRequiredDirectorySizes(cus.Disk, 5*datasizes.GiB)
	assert.EqualError(t, err, `unknown disk customization type "rando"`)
}

func TestGenPartitionTableDiskCustomizationSizes(t *testing.T) {
	rng := createRand()

	for _, tc := range []struct {
		name                string
		rootfsMinSize       uint64
		partitions          []blueprint.PartitionCustomization
		expectedMinRootSize datasizes.Size
	}{
		{
			"empty disk customizaton, root expands to rootfsMinsize",
			2 * datasizes.GiB,
			nil,
			2 * datasizes.GiB,
		},
		// plain
		{
			"plain, no root minsize, expands to rootfsMinSize",
			5 * datasizes.GiB,
			[]blueprint.PartitionCustomization{
				{
					MinSize: 10 * datasizes.GiB,
					FilesystemTypedCustomization: blueprint.FilesystemTypedCustomization{
						Mountpoint: "/var",
						FSType:     "xfs",
					},
				},
			},
			5 * datasizes.GiB,
		},
		{
			"plain, small root minsize, expands to rootfsMnSize",
			5 * datasizes.GiB,
			[]blueprint.PartitionCustomization{
				{
					MinSize: 1 * datasizes.GiB,
					FilesystemTypedCustomization: blueprint.FilesystemTypedCustomization{
						Mountpoint: "/",
						FSType:     "xfs",
					},
				},
			},
			5 * datasizes.GiB,
		},
		{
			"plain, big root minsize",
			5 * datasizes.GiB,
			[]blueprint.PartitionCustomization{
				{
					MinSize: 10 * datasizes.GiB,
					FilesystemTypedCustomization: blueprint.FilesystemTypedCustomization{
						Mountpoint: "/",
						FSType:     "xfs",
					},
				},
			},
			10 * datasizes.GiB,
		},
		// btrfs
		{
			"btrfs, no root minsize, expands to rootfsMinSize",
			5 * datasizes.GiB,
			[]blueprint.PartitionCustomization{
				{
					Type:    "btrfs",
					MinSize: 10 * datasizes.GiB,
					BtrfsVolumeCustomization: blueprint.BtrfsVolumeCustomization{
						Subvolumes: []blueprint.BtrfsSubvolumeCustomization{
							{
								Mountpoint: "/var",
								Name:       "varvol",
							},
						},
					},
				},
			},
			5 * datasizes.GiB,
		},
		{
			"btrfs, small root minsize, expands to rootfsMnSize",
			5 * datasizes.GiB,
			[]blueprint.PartitionCustomization{
				{
					Type:    "btrfs",
					MinSize: 1 * datasizes.GiB,
					BtrfsVolumeCustomization: blueprint.BtrfsVolumeCustomization{
						Subvolumes: []blueprint.BtrfsSubvolumeCustomization{
							{
								Mountpoint: "/",
								Name:       "rootvol",
							},
						},
					},
				},
			},
			5 * datasizes.GiB,
		},
		{
			"btrfs, big root minsize",
			5 * datasizes.GiB,
			[]blueprint.PartitionCustomization{
				{
					Type:    "btrfs",
					MinSize: 10 * datasizes.GiB,
					BtrfsVolumeCustomization: blueprint.BtrfsVolumeCustomization{
						Subvolumes: []blueprint.BtrfsSubvolumeCustomization{
							{
								Mountpoint: "/",
								Name:       "rootvol",
							},
						},
					},
				},
			},
			10 * datasizes.GiB,
		},
		// lvm
		{
			"lvm, no root minsize, expands to rootfsMinSize",
			5 * datasizes.GiB,
			[]blueprint.PartitionCustomization{
				{
					Type:    "lvm",
					MinSize: 10 * datasizes.GiB,
					VGCustomization: blueprint.VGCustomization{
						LogicalVolumes: []blueprint.LVCustomization{
							{
								MinSize: 10 * datasizes.GiB,
								FilesystemTypedCustomization: blueprint.FilesystemTypedCustomization{
									Mountpoint: "/var",
									FSType:     "xfs",
								},
							},
						},
					},
				},
			},
			5 * datasizes.GiB,
		},
		{
			"lvm, small root minsize, expands to rootfsMnSize",
			5 * datasizes.GiB,
			[]blueprint.PartitionCustomization{
				{
					Type:    "lvm",
					MinSize: 1 * datasizes.GiB,
					VGCustomization: blueprint.VGCustomization{
						LogicalVolumes: []blueprint.LVCustomization{
							{
								MinSize: 1 * datasizes.GiB,
								FilesystemTypedCustomization: blueprint.FilesystemTypedCustomization{
									Mountpoint: "/",
									FSType:     "xfs",
								},
							},
						},
					},
				},
			},
			5 * datasizes.GiB,
		},
		{
			"lvm, big root minsize",
			5 * datasizes.GiB,
			[]blueprint.PartitionCustomization{
				{
					Type:    "lvm",
					MinSize: 10 * datasizes.GiB,
					VGCustomization: blueprint.VGCustomization{
						LogicalVolumes: []blueprint.LVCustomization{
							{
								MinSize: 10 * datasizes.GiB,
								FilesystemTypedCustomization: blueprint.FilesystemTypedCustomization{
									Mountpoint: "/",
									FSType:     "xfs",
								},
							},
						},
					},
				},
			},
			10 * datasizes.GiB,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imgType := NewTestBootcImageType(t, "qcow2")

			rootfsMinsize := tc.rootfsMinSize
			cus := &blueprint.Customizations{
				Disk: &blueprint.DiskCustomization{
					Partitions: tc.partitions,
				},
			}
			pt, err := imgType.genPartitionTable(cus, rootfsMinsize, rng)
			assert.NoError(t, err)

			var rootSize datasizes.Size
			err = pt.ForEachMountable(func(mnt disk.Mountable, path []disk.Entity) error {
				if mnt.GetMountpoint() == "/" {
					for idx := len(path) - 1; idx >= 0; idx-- {
						if parent, ok := path[idx].(disk.Sizeable); ok {
							rootSize = parent.GetSize()
							break
						}
					}
				}
				return nil
			})
			assert.NoError(t, err)
			// expected size is within a reasonable limit
			assert.True(t, rootSize >= tc.expectedMinRootSize && rootSize < tc.expectedMinRootSize+5*datasizes.MiB)
		})
	}
}

func TestManifestFilecustomizationsSad(t *testing.T) {
	imgType := NewTestBootcImageType(t, "qcow2")
	bp := &blueprint.Blueprint{
		Customizations: &blueprint.Customizations{
			Files: []blueprint.FileCustomization{
				{
					Path: "/not/allowed",
					Data: "some-data",
				},
			},
		},
	}

	_, _, err := imgType.Manifest(bp, distro.ImageOptions{}, nil, common.ToPtr(int64(0)))
	assert.EqualError(t, err, `the following custom files are not allowed: ["/not/allowed"]`)
}

func TestManifestDirCustomizationsSad(t *testing.T) {
	imgType := NewTestBootcImageType(t, "qcow2")
	bp := &blueprint.Blueprint{
		Customizations: &blueprint.Customizations{
			Directories: []blueprint.DirectoryCustomization{
				{
					Path: "/dir/not/allowed",
				},
			},
		},
	}

	_, _, err := imgType.Manifest(bp, distro.ImageOptions{}, nil, common.ToPtr(int64(0)))
	assert.EqualError(t, err, `the following custom directories are not allowed: ["/dir/not/allowed"]`)
}

func TestGenPartitionTableFromOSInfo(t *testing.T) {
	var bp blueprint.Blueprint
	imgType := NewTestBootcImageType(t, "qcow2")
	// pretend a custom partition table is set via the bootc
	// container sourceInfo mechanism
	newPt, err := imgType.BasePartitionTable()
	assert.NoError(t, err)
	newPt.UUID = "01010101-01011-01011-01011-01010101"
	d := imgType.arch.distro.(*BootcDistro)
	d.sourceInfo.PartitionTable = newPt

	// validate that the container uuid is part of the generated
	// manifest
	mf, _, err := imgType.Manifest(&bp, distro.ImageOptions{}, nil, common.ToPtr(int64(0)))
	assert.NoError(t, err)
	manifestJson, err := mf.Serialize(nil, diskContainers, nil, nil, nil)
	assert.NoError(t, err)
	assert.Contains(t, string(manifestJson), "01010101-01011-01011-01011-01010101")
}

// Each bootc manifestFor* variant hand-copies customizations, so pin that
// every variant does not forget to pass Subscription through.
func TestManifestSubscriptionCustomization(t *testing.T) {
	for _, imgTypeName := range []string{"qcow2", "pxe-tar-xz"} {
		t.Run(imgTypeName, func(t *testing.T) {
			imgType := NewTestBootcImageType(t, imgTypeName)
			imgOpts := distro.ImageOptions{
				Subscription: &subscription.ImageOptions{
					Organization:  "2040324",
					ActivationKey: "my-secret-key",
				},
			}

			mf, _, err := imgType.Manifest(&blueprint.Blueprint{}, imgOpts, nil, common.ToPtr(int64(0)))
			assert.NoError(t, err)
			manifestJson, err := mf.Serialize(nil, diskContainers, nil, nil, nil)
			assert.NoError(t, err)

			assert.Contains(t, string(manifestJson), "osbuild-subscription-register.service")
			assert.Contains(t, string(manifestJson), "/etc/osbuild-subscription-register.env")
		})
	}
}

func TestManifestPXEComposefsUnsupported(t *testing.T) {
	imgType := NewTestBootcImageType(t, "pxe-tar-xz")
	_, _, err := imgType.Manifest(&blueprint.Blueprint{}, distro.ImageOptions{}, nil, common.ToPtr(int64(0)))
	require.NoError(t, err)

	imgType.arch.distro.(*BootcDistro).composefs = true
	_, _, err = imgType.Manifest(&blueprint.Blueprint{}, distro.ImageOptions{}, nil, common.ToPtr(int64(0)))
	assert.EqualError(t, err, `image type "pxe-tar-xz" is not supported for bootc containers that select the composefs backend`)
}

func TestManifestComposefsCustomizationsWarn(t *testing.T) {
	const ukiWarnPrefix = `blueprint validation failed for image type "qcow2": the bootc container has a unified kernel (UKI), which does not support: `
	const composefsWarnPrefix = `blueprint validation failed for image type "qcow2": the bootc container selects the composefs backend, which does not support: `
	rootPart := blueprint.PartitionCustomization{
		Type:                         "plain",
		FilesystemTypedCustomization: blueprint.FilesystemTypedCustomization{Mountpoint: "/", FSType: "ext4"},
	}
	dataPart := blueprint.PartitionCustomization{
		Type:                         "plain",
		MinSize:                      datasizes.GiB,
		FilesystemTypedCustomization: blueprint.FilesystemTypedCustomization{Mountpoint: "/var/data", FSType: "ext4"},
	}

	for name, tc := range map[string]struct {
		customizations *blueprint.Customizations
		options        distro.ImageOptions
		expected       string
		// only a unified kernel does not support it
		ukiOnly bool
	}{
		"empty": {},
		"disk-root-only": {
			customizations: &blueprint.Customizations{
				Disk: &blueprint.DiskCustomization{Partitions: []blueprint.PartitionCustomization{rootPart}},
			},
		},
		"user": {
			customizations: &blueprint.Customizations{User: []blueprint.UserCustomization{{Name: "alice"}}},
			expected:       "customizations.user",
		},
		"kargs": {
			customizations: &blueprint.Customizations{Kernel: &blueprint.KernelCustomization{Append: "debug"}},
			expected:       "customizations.kernel.append",
			ukiOnly:        true,
		},
		"group": {
			customizations: &blueprint.Customizations{Group: []blueprint.GroupCustomization{{Name: "wheel2"}}},
			expected:       "customizations.group",
		},
		"files-and-dirs": {
			customizations: &blueprint.Customizations{
				Directories: []blueprint.DirectoryCustomization{{Path: "/etc/foo"}},
				Files:       []blueprint.FileCustomization{{Path: "/etc/foo/bar", Data: "baz"}},
			},
			expected: "customizations.directories, customizations.files",
		},
		"ignition": {
			customizations: &blueprint.Customizations{
				Ignition: &blueprint.IgnitionCustomization{FirstBoot: &blueprint.FirstBootIgnitionCustomization{ProvisioningURL: "https://example.com/config.ign"}},
			},
			expected: "customizations.ignition",
		},
		"bootloader": {
			customizations: getBootloaderConfig().Customizations,
			expected:       "customizations.bootloader",
		},
		"subscription": {
			options: distro.ImageOptions{
				Subscription: &subscription.ImageOptions{Organization: "2040324", ActivationKey: "my-secret-key"},
			},
			expected: "subscription",
		},
		"disk-extra-mountpoint": {
			customizations: &blueprint.Customizations{
				Disk: &blueprint.DiskCustomization{Partitions: []blueprint.PartitionCustomization{rootPart, dataPart}},
			},
			expected: "customizations.disk mountpoints without an fstab (/var/data)",
		},
		"filesystem-extra-mountpoint": {
			customizations: &blueprint.Customizations{
				Filesystem: []blueprint.FilesystemCustomization{{Mountpoint: "/", MinSize: datasizes.GiB}, {Mountpoint: "/var/data", MinSize: datasizes.GiB}},
			},
			expected: "customizations.filesystem mountpoints without an fstab (/var/data)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			imgType := NewTestBootcImageType(t, "qcow2")
			bp := &blueprint.Blueprint{Customizations: tc.customizations}

			bd := imgType.arch.distro.(*BootcDistro)

			// only look at the composefs warnings, e.g.
			// customizations.filesystem is not a supported option for
			// bootc disks in the first place
			composefsWarnings := func() []string {
				_, warnings, err := imgType.Manifest(bp, tc.options, nil, common.ToPtr(int64(0)))
				require.NoError(t, err)
				return slices.DeleteFunc(warnings, func(w string) bool {
					return !strings.HasPrefix(w, ukiWarnPrefix) && !strings.HasPrefix(w, composefsWarnPrefix)
				})
			}

			// with ostree all of these are applied
			assert.Empty(t, composefsWarnings())

			bd.composefs = true
			if tc.expected == "" || tc.ukiOnly {
				assert.Empty(t, composefsWarnings())
			} else {
				assert.Equal(t, []string{composefsWarnPrefix + tc.expected}, composefsWarnings())
			}

			// a unified kernel implies composefs, with or without the
			// install configuration
			for _, composefs := range []bool{true, false} {
				bd.composefs = composefs
				bd.unifiedKernel = true
				if tc.expected == "" {
					assert.Empty(t, composefsWarnings())
				} else {
					assert.Equal(t, []string{ukiWarnPrefix + tc.expected}, composefsWarnings())
				}
			}
		})
	}
}
