# just test that the digest is stripped
FROM cgr.dev/ORG/python:3.12-dev
USER root

RUN apk add -U gettext git libpq make rsync
