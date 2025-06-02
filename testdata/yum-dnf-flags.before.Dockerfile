# Make sure when the -y flag is used before the install keyword
# that conversion still occurs correctly
FROM fedora:30

RUN yum update -y && \
    yum -y install httpd php php-cli php-common && \
    yum clean all && \
    rm -rf /var/cache/yum/*

RUN dnf update -y && \
    dnf -y install httpd php php-cli php-common && \
    dnf clean all

RUN microdnf update -y && \
    microdnf -y install httpd php php-cli php-common && \
    microdnf clean all
