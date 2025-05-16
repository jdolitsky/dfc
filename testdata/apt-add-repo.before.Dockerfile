FROM debian:bookworm
RUN apt-get update \
    && apt-get install -y software-properties-common=0.99.22.9 \
    && add-apt-repository ppa:libreoffice/libreoffice-still \
    && apt-get install -y libreoffice \
    && apt-get clean