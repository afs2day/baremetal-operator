FROM registry.svc.ci.openshift.org/openshift/release:golang-1.13 AS builder
WORKDIR /go/src/github.com/metal3-io/baremetal-operator
COPY . .
RUN make build
RUN make tools
RUN make wait-for-ironic

FROM base
COPY --from=builder /go/src/github.com/metal3-io/baremetal-operator/build/_output/bin/baremetal-operator /
COPY --from=builder /go/src/github.com/metal3-io/baremetal-operator/build/_output/bin/get-hardware-details /
COPY --from=builder /go/src/github.com/metal3-io/baremetal-operator/build/_output/bin/wait-for-ironic /
RUN if ! rpm -q genisoimage; then yum install -y genisoimage && yum clean all && rm -rf /var/cache/yum/*; fi
