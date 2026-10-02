package osbuild_test

import (
	"encoding/json"
	"testing"

	"github.com/osbuild/image-builder/pkg/osbuild"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootcDeploymentMountSerialized(t *testing.T) {
	mnt := osbuild.NewBootcDeploymentMount("some-name", osbuild.OSTreeMountSourceMount)
	json, err := json.MarshalIndent(mnt, "", "  ")
	require.Nil(t, err)
	assert.Equal(t, `
{
  "name": "some-name",
  "type": "org.osbuild.bootc.deployment",
  "options": {
    "source": "mount"
  }
}`[1:], string(json))
}
