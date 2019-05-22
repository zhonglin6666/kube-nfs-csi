FROM centos:7.4.1708

RUN cp -a /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
  && yum -y install nfs-utils \
  && yum -y install epel-release \
  && yum -y install jq \
  && yum clean all \
  && mkdir /persistentvolumes

COPY bin/nfsplugin /nfsplugin

ENTRYPOINT ["/nfsplugin"]
