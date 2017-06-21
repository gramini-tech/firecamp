#!/bin/sh
set -x
set -e

# run all unit tests
gotest="go test -short -cover"

$gotest ./containersvc/...
$gotest ./db/...
$gotest ./dns/...
$gotest ./manage/...
$gotest ./utils/...

# dockervolume and server unit tests use loop device, root permission is required.
sudo -E $gotest ./dockervolume/...

# to exclude ec2 test, switch to run server unit test only
sudo -E $gotest ./server/...
#cd server; sudo -E go test; cd -
