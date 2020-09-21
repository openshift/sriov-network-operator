echo '# SR-iOV Operator RDMA service

RETRIES=100
WAIT_TIME=1

new_rdma_mode=

rdma_mode=$(rdma system | cut -c7- | grep "[a-z]")

if [ "$rdma_mode" = "$new_rdma_mode" ]; then
  exit 0
fi

for _ in $(seq 0 $RETRIES); do
  rdma system set netns "$new_rdma_mode"
  ret_val=$?
  if [ "$ret_val" -eq 0 ]; then
    exit 0
  fi
  sleep $WAIT_TIME
done' > /etc/init.d/sriov-network-rdma.sh

rdma_mode=$(rdma system | cut -c7- | grep "[a-z]")

sed -i 's/new_rdma_mode=.*/new_rdma_mode='$rdma_mode'/g' /etc/init.d/sriov-network-rdma.sh

cat > /usr/lib/systemd/system/sriov-network-operator-rdma.service <<EOF
[Unit]
Description=sriov-network-operator-rdma: Used by sriov-network-operator to change the rdma mode
After=network-pre.target

[Service]
ExecStart=/bin/sh /etc/init.d/sriov-network-rdma.sh

[Install]
WantedBy=multi-user.target
EOF

systemctl enable sriov-network-operator-rdma
