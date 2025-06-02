# From https://luis-sena.medium.com/creating-the-perfect-python-dockerfile-51bdec41f1c8
# This Dockerfile demonstrates a multi-stage Python build with virtual environment

# using ubuntu LTS version
FROM cgr.dev/ORG/chainguard-base:latest AS builder-image
USER root

RUN apk add --no-cache build-base py3-pip py3-wheel python3.9 python3.9-dev python3.9-venv

# create and activate virtual environment
RUN python3.9 -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# install requirements
COPY requirements.txt .
RUN pip3 install --no-cache-dir -r requirements.txt

FROM cgr.dev/ORG/chainguard-base:latest AS runner-image
USER root
RUN apk add --no-cache python3-venv python3.9

COPY --from=builder-image /opt/venv /opt/venv

# activate virtual environment
ENV VIRTUAL_ENV=/opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# Add non-root user
RUN adduser appuser
USER appuser
WORKDIR /home/appuser

# Copy application code
COPY --chown=appuser:appuser . .

CMD ["python", "app.py"] 