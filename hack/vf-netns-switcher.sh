#!/bin/bash

conf_file=""

declare -a netnses

declare -A pfs
declare -A pcis
declare -A pf_port_names
declare -A pf_switch_ids

TIMEOUT="${TIMEOUT:-2}"
POLL_INTERVAL="${POLL_INTERVAL:-1}"

while test $# -gt 0; do
  case "$1" in

   --netns | -n)
      input=$2
      local_netns=$(echo $input | cut -s -d ':' -f 1)
      local_pfs=$(echo $input | cut -s -d ':' -f 2)
      input=""

      if [[ -z "$local_netns" ]];then
          echo "Error: flag --netns specified but netns is empty, please \
provide it in the form --netns <netns>:<pf1>,<pf2> !"
          echo "Exiting!"
          exit 1
      fi

      if [[ -z "$local_pfs" ]];then
          echo "Error: flag --netns specified but pfs is empty, please \
provide it in the form --netns <netns>:<pf1>,<pf2> !"
          echo "Exiting!"
          exit 1
      fi

      netnses+=("$local_netns")

      pfs["$local_netns"]="$(echo $local_pfs | tr , " ")"

      local_netns=""
      local_pfs=""

      shift
      shift
      ;;

   --conf-file | -c)
      conf_file=$2
      if [[ ! -f "$conf_file" ]];then
          echo "Error: flag --conf-file specified but file $conf_file \
not found!"
          exit 1
      fi

      shift
      shift
      ;;

   --help | -h)
      echo "
vf-netns-switcher.sh --netns <netns>:<pf1>,<pf2> [--conf-file <>]:

	--netns | -n		The netns and its interfaces to switch the interfaces PFs and VFs to. \
It must be of the form <netns>:<pf1>,<pf2>. This flag can be repeated to specify more netnses.

	--conf-file | -c	A file to read confs from, this will override cli flags. Conf file should be of the form:
				[
				  {
				    "netns": <netns1>,
				    "pfs": [
				      "pf1",
				      "pf2"
				    ]
				  },
				  {
				    "netns": <netns2>,
				    "pfs": [
				      "pf3",
				      "pf4"
				    ]
				  }
				]

"
      exit 0
      ;;

   *)
      echo "Error: invalid option: $1!"
      echo "Exiting..."
      exit 1
  esac
done

return_interfaces_to_default_namespace(){
    for netns in ${netnses[@]};do
        for pf in ${pfs[$netns]};do
            return_interface_to_default_namespace "${netns}" "${pf}"
        done
    done

    exit
}

return_interface_to_default_namespace(){
    local netns="$1"
    local interface="$2"

    local reset_interface_netns="${interface}-reset"

    local status=0

    ip netns add "${reset_interface_netns}"
    sleep 1

    if ! ip netns exec "${netns}" ip link set "${interface}" netns "${reset_interface_netns}";then
        echo "ERROR: unable to switch interface ${interface} from netns ${netns} to netns ${reset_interface_netns}!"
        status=1
    fi

    ip netns del "${reset_interface_netns}"

    ip link set "${interface}" up

    return ${status}
}

get_pcis_from_pfs(){
    local worker_netns="$1"
    shift
    local interfaces="$@"
    for interface in $interfaces; do
        pcis["$interface"]="$(get_pci_from_net_name "$interface" "$worker_netns")"
    done
}

get_pci_from_net_name(){
    local interface_name=$1
    local worker_netns="$2"

    if [[ -z "$(ip l show $interface_name)" ]];then
        if [[ -n "$(docker exec -t ${worker_netns} ip l show $interface_name)" ]];then
            ip netns exec ${worker_netns} bash -c "basename \$(readlink /sys/class/net/${interface_name}/device)"
            return 0
        fi
        echo ""
        return 1
    fi

    basename $(readlink /sys/class/net/${interface_name}/device)
    return 0
}

netns_create(){
    local worker_netns="$1"

    if [[ ! -e /var/run/netns/$worker_netns ]];then
        local pid="$(docker inspect -f '{{.State.Pid}}' $worker_netns)"

        if [[ -z "$pid" ]];then
            return 1
        fi

        mkdir -p /var/run/netns/
        rm -rf /var/run/netns/$worker_netns
        ln -sf /proc/$pid/ns/net "/var/run/netns/$worker_netns"

        if [[ -z "$(ip netns | grep $worker_netns)" ]];then
            return 1
        fi
    fi
    return 0
}

switch_pfs(){
    local worker_netns="$1"
    shift
    local interfaces="$@"

    echo "Switching \"$interfaces\" into $worker_netns ..."

    for pf in $interfaces;do
        switch_pf "$pf" "$worker_netns"
    done
}

switch_pf(){
    local pf_name="$1"
    local worker_netns="$2"

    if [[ -z "$(ip netns | grep ${worker_netns})" ]];then
        echo "Error: Namespace $worker_netns not found!"
        return 1
    fi

    if [[ -z "$(ip l show ${pf_name})" ]];then
        if [[ -z "$(docker exec -t ${worker_netns} ip l show ${pf_name})" ]];then
            echo "Error: Interface $pf_name not found..."
            return 1
        fi

        echo "PF ${pf_name} already in namespace $worker_netns!"
    else
        if ! ip l set dev $pf_name netns $worker_netns;then
            echo "Error: unable to set $pf_name namespace to $worker_netns!"
            return 1
        fi
    fi

    if ! docker exec -t ${worker_netns} ip l set $pf_name up;then
        echo "Error: unable to set $pf_name to up!"
        return 1
    fi

    return 0
    
}

switch_vf(){
    local vf_name="$1"
    local worker_netns="$2"

    if [[ -z "$(ip l show $vf_name)" ]];then
        return 1
    fi

    if ip link set "$vf_name" netns "$worker_netns"; then
        if timeout "$TIMEOUT"s bash -c "until ip netns exec $worker_netns ip link show $vf_name > /dev/null; do sleep $POLL_INTERVAL; done"; then
            return 0
        else
            return 1
        fi
    else
        return 1
    fi
}

switch_netns_vfs(){
    local worker_netns="$1"

    for pf in ${pfs["$worker_netns"]};do
        echo "Switching interface $pf vfs into $worker_netns...."
        switch_interface_vfs "$pf" "$worker_netns" "${pcis[$pf]}"
    done
}

get_pf_switch_dev_info(){
   local worker_netns="$1"
    shift
    local interfaces="$@"
    for interface in $interfaces; do
        interface_pci_address="${pcis[$interface]}"
        if grep -q 'switchdev' <(devlink dev eswitch show pci/$interface_pci_address ); then
          continue
        fi
        pf_port_names["$interface"]="$(cat /sys/class/net/${interface}/phys_port_name)"
        pf_switch_ids["$interface"]="$(cat /sys/class/net/${interface}/phys_switch_id)"
    done
}

switch_netns_vf_representors(){
    local worker_netns="$1"
    for pf in ${pfs["$worker_netns"]};do
        echo "Switching pf $pf vf representors into $worker_netns ..."
        switch_interface_vf_representors "$pf" "$worker_netns"
    done
}

switch_interface_vf_representors(){
    local pf_name="$1"
    local worker_netns=$2

    if [[ -z "${pf_switch_ids[$pf_name]}" ]] || [[ -z ${pf_port_names[$pf_name]:1} ]];then
        echo "$pf_name does not have pf_switch_id or pf_port_name, assuming not switchdev..."
        return 0
    fi

    for interface in $(ls /sys/class/net);do
        phys_switch_id=$(cat /sys/class/net/$interface/phys_switch_id)
        if [[ "$phys_switch_id" != "${pf_switch_ids[$pf_name]}" ]]; then
            continue
        fi
        phys_port_name=$(cat /sys/class/net/$interface/phys_port_name)
        phys_port_name_pf_index=${phys_port_name%vf*}
        phys_port_name_pf_index=${phys_port_name_pf_index#pf}
        if [[ "$phys_port_name_pf_index" != "${pf_port_names[$pf_name]:1}"  ]]; then
            continue
        fi
        echo "Switching VF representor $interface of PF $pf_name to netns $worker_netns"
        switch_vf $interface $worker_netns
    done
}

switch_interface_vfs(){
    local pf_name="$1"
    local worker_netns="$2"
    local pci="$3"

    vfs_list=$(ls /sys/bus/pci/devices/$pci | grep virtfn)

    if [[ -z "${vfs_list}" ]];then
        echo "Warning: No VFs found for interface $pf_name!!"
        return 0
    fi

    for vf in $vfs_list;do
        local vf_interface="$(ls /sys/bus/pci/devices/$pci/$vf/net)"

        if [[ -n "$vf_interface" ]];then
            echo "Switching $vf_interface to namespace $worker_netns..."
            sleep 2
            if ! switch_vf "$vf_interface" "$worker_netns";then
                echo "Error: could not switch $vf_interface to namespace $worker_netns!"
            else
                echo "Successfully switched $vf_interface to namespace $worker_netns"
            fi
        fi
    done
}

read_confs(){
    local conf_file="$1"

    let number_of_netns=$(jq length "${conf_file}")-1

    for index in $(seq 0 $number_of_netns);do
        netnses+=("$(jq -r .[${index}].netns $conf_file)")
        let number_of_pfs=$(jq .[$index].pfs $conf_file | jq length)-1
        for pf_index in $(seq 0 $number_of_pfs);do
            pfs[${netnses[-1]}]+="$(jq -r .[$index].pfs[$pf_index] $conf_file) "
        done
    done
}

variables_check(){
    local status=0

    check_empty_var "netnses"
    let status=$status+$?
    check_empty_var "pfs"
    let status=$status+$?

    return $status
}

check_empty_var(){
    local var_name="$1"

    if [[ -z "${!var_name[@]}" ]];then
        echo "Error: $var_name is empty..."
        return 1
    fi

    return 0
}

main(){
    trap return_interfaces_to_default_namespace INT EXIT TERM

    while true;do
        for netns in ${netnses[@]};do
            switch_pfs "$netns" "${pfs[$netns]}"
            sleep 2
            switch_netns_vfs "$netns"
            sleep 2
            switch_netns_vf_representors "$netns"
        done
        sleep $TIMEOUT
    done
}

if [[ -n "$conf_file" ]];then
    unset netnses
    unset pfs

    declare -a netnses
    declare -A pfs

    read_confs "$conf_file"
fi

variables_check
let status=$?
if [[ "$status" != "0" ]];then
    echo "Error: empty var..."
    exit $status
fi

for netns in ${netnses[@]};do
    netns_create "$netns"
    let status=$status+$?
    if [[ "$status" != "0" ]];then
        echo "Error: failed to create netns..."
        exit $status
    fi
done

for netns in ${netnses[@]};do
    get_pcis_from_pfs "$netns" "${pfs[$netns]}"
    get_pf_switch_dev_info "$netns" "${pfs[$netns]}"
done

if [[ "${#pcis[@]}" == "0" ]];then
    echo "Error: could not get pci addresses of interfaces ${pfs[@]}!!"
    exit 1
fi

main
