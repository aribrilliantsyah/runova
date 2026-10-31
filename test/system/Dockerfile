# A plain Ubuntu 26.04 with systemd as PID 1, sshd, and a sudo user: the
# "fresh server" that test/system/run.sh installs Runova on.
FROM ubuntu:26.04

ENV DEBIAN_FRONTEND=noninteractive
# The image blocks service starts during apt (policy-rc.d); a real server does not.
RUN rm -f /usr/sbin/policy-rc.d \
 && apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv dbus openssh-server sudo iproute2 ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && systemctl enable ssh \
 && systemctl mask getty.target console-getty.service systemd-logind.service systemd-firstboot.service \
 && useradd --create-home --shell /bin/bash --groups sudo deploy \
 && echo 'deploy ALL=(ALL) NOPASSWD:ALL' >/etc/sudoers.d/deploy \
 && chmod 0440 /etc/sudoers.d/deploy \
 && mkdir -p /home/deploy/.ssh \
 && chmod 0700 /home/deploy/.ssh \
 && chown deploy:deploy /home/deploy/.ssh

STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
