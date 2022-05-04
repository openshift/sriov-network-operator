#!/bin/bash


SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
SUT_SCRIPT="${SCRIPTPATH}/../../bindata/scripts/enable-kargs.sh"


test_RpmOstree_Add_All_Arguments() {
    echo "a b c=d eee=fff" > ${FAKE_HOST}/proc/cmdline
    touch ${FAKE_HOST}/run/ostree-booted

    output=`$SUT_SCRIPT X=Y W=Z`
    assertEquals 0 $?
    assertEquals "2" $output

    assertContains "`cat ${FAKE_HOST}/rpm-ostree_calls`" "--append X=Y"
    assertContains "`cat ${FAKE_HOST}/rpm-ostree_calls`" "--append W=Z"
}


test_RpmOstree_Add_Only_Missing_Arguments() {
    echo "a b c=d eee=fff K=L" > ${FAKE_HOST}/proc/cmdline
    touch ${FAKE_HOST}/run/ostree-booted

    output=`$SUT_SCRIPT K=L X=Y`
    assertEquals 0 $?
    assertEquals "1" $output

    assertContains "`cat ${FAKE_HOST}/rpm-ostree_calls`" "--append X=Y"
    assertNotContains "`cat ${FAKE_HOST}/rpm-ostree_calls`" "--append K=L"
}


###### Mock /host directory ######
export FAKE_HOST="$(mktemp -d)"
trap 'rm -rf -- "$FAKE_HOST"' EXIT

setUp() {
    mkdir -p                            ${FAKE_HOST}/{usr/bin,etc,proc,run}
    cp $(which cat)                     ${FAKE_HOST}/usr/bin/
    cp $(which test)                    ${FAKE_HOST}/usr/bin/
    cp $(which sh)                      ${FAKE_HOST}/usr/bin/
    cp "$SCRIPTPATH/rpm-ostree_mock"    ${FAKE_HOST}/usr/bin/rpm-ostree
}

# Mock chroot calls to the temporary test folder
export real_chroot=$(which chroot)
chroot() {
    $real_chroot $FAKE_HOST ${@:2}
}
export -f chroot


source ${SCRIPTPATH}/shunit2
