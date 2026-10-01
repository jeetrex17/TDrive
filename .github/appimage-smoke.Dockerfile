FROM ubuntu:22.04

# Deliberately omit GTK and WebKit: the AppImage must provide both.
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
       ca-certificates dbus-x11 fonts-dejavu-core imagemagick libegl1 libgl1 \
       libgl1-mesa-dri procps x11-apps x11-utils xdotool xvfb \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 1000 smoke

COPY smoke-appimage.sh /usr/local/bin/smoke-appimage
RUN chmod 755 /usr/local/bin/smoke-appimage
USER smoke
WORKDIR /home/smoke
ENTRYPOINT ["/usr/local/bin/smoke-appimage"]
