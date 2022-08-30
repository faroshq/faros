ARG TARGET_GOOS
ARG TARGET_GOARCH
FROM golang:1.18 as builder
ENV GO111MODULE=on \
    CGO_ENABLED=0 \
    TARGET_GOOS=${TARGET_GOOS} \
    TARGET_GOARCH=${TARGET_GOARCH}
WORKDIR /app

RUN apt-get install -y gcc g++ git openssh-client make
# <- COPY go.mod and go.sum files to the workspace
COPY go.mod .
COPY go.sum .

# COPY the source code as the last step
COPY . .

RUN make faros

FROM alpine
RUN apk --update add ca-certificates

COPY --from=builder /app/release/faros/faros /bin

# Create a group and user
ENV USER_ID=1000
ENV GROUP_ID=1000
ENV USER_NAME=faros
ENV GROUP_NAME=farosgroup

RUN addgroup -g $USER_ID $GROUP_NAME && \
    adduser --shell /sbin/nologin --disabled-password \
    --no-create-home --uid $USER_ID --ingroup $GROUP_NAME $USER_NAME

RUN chown -R $USER_NAME:$GROUP_NAME /bin/faros

USER $USER_ID

ENTRYPOINT ["/bin/faros"]
