FROM busybox
ADD bin/etcd2-bootstrapper-linux-amd64 /etcd2-bootstrapper
ENTRYPOINT [ "/etcd2-bootstrapper" ]
