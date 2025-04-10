/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package dfc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Empty default mappings for tests
var emptyMappings = MappingsConfig{
	Images:   map[string]string{},
	Packages: PackageMap{},
}

func TestParseConvert(t *testing.T) {
	convertTests := []struct {
		name     string
		raw      string
		expected *Dockerfile
	}{
		{
			name: "apt-get basic example",
			raw:  `RUN apt-get update && apt-get install -y nginx`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN apt-get update && apt-get install -y nginx`,
						Converted: `RUN apk add --no-cache nginx`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"nginx"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command: "apt-get",
											Args:    []string{"install", "-y", "nginx"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "nginx"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "useradd basic example",
			raw:  `RUN ` + CommandUserAdd + ` myuser`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN ` + CommandUserAdd + ` myuser`,
						Converted: `RUN ` + CommandAddUser + ` myuser`,
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: CommandUserAdd,
											Args:    []string{"myuser"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: CommandAddUser,
											Args:    []string{"myuser"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "useradd with options",
			raw:  `RUN ` + CommandUserAdd + ` -m -s /bin/bash -u 1001 -g mygroup myuser`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN ` + CommandUserAdd + ` -m -s /bin/bash -u 1001 -g mygroup myuser`,
						Converted: `RUN ` + CommandAddUser + ` --shell /bin/bash --uid 1001 --ingroup mygroup myuser`,
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: CommandUserAdd,
											Args:    []string{"-m", "-s", "/bin/bash", "-u", "1001", "-g", "mygroup", "myuser"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: CommandAddUser,
											Args:    []string{"--shell", "/bin/bash", "--uid", "1001", "--ingroup", "mygroup", "myuser"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "groupadd basic example",
			raw:  `RUN ` + CommandGroupAdd + ` mygroup`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN ` + CommandGroupAdd + ` mygroup`,
						Converted: `RUN ` + CommandAddGroup + ` mygroup`,
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: CommandGroupAdd,
											Args:    []string{"mygroup"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: CommandAddGroup,
											Args:    []string{"mygroup"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "groupadd with options",
			raw:  `RUN ` + CommandGroupAdd + ` -r -g 1001 mygroup`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN ` + CommandGroupAdd + ` -r -g 1001 mygroup`,
						Converted: `RUN ` + CommandAddGroup + ` --system --gid 1001 mygroup`,
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: CommandGroupAdd,
											Args:    []string{"-r", "-g", "1001", "mygroup"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: CommandAddGroup,
											Args:    []string{"--system", "--gid", "1001", "mygroup"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple commands with useradd",
			raw:  `RUN ` + CommandGroupAdd + ` -r appgroup && ` + CommandUserAdd + ` -r -g appgroup appuser`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw: `RUN ` + CommandGroupAdd + ` -r appgroup && ` + CommandUserAdd + ` -r -g appgroup appuser`,
						Converted: `RUN ` + CommandAddGroup + ` --system appgroup && \
    ` + CommandAddUser + ` --system --ingroup appgroup appuser`,
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   CommandGroupAdd,
											Args:      []string{"-r", "appgroup"},
											Delimiter: "&&",
										},
										{
											Command: CommandUserAdd,
											Args:    []string{"-r", "-g", "appgroup", "appuser"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   CommandAddGroup,
											Args:      []string{"--system", "appgroup"},
											Delimiter: "&&",
										},
										{
											Command: CommandAddUser,
											Args:    []string{"--system", "--ingroup", "appgroup", "appuser"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multi-line RUN command",
			raw:  `RUN apt-get update && apt-get install -y nginx curl vim`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN apt-get update && apt-get install -y nginx curl vim`,
						Converted: `RUN apk add --no-cache curl nginx vim`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"curl", "nginx", "vim"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command: "apt-get",
											Args:    []string{"install", "-y", "nginx", "curl", "vim"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "curl", "nginx", "vim"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multi-line RUN command with continuation",
			raw:  `RUN apt-get update && apt-get install -y nginx`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN apt-get update && apt-get install -y nginx`,
						Converted: `RUN apk add --no-cache nginx`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"nginx"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command: "apt-get",
											Args:    []string{"install", "-y", "nginx"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "nginx"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "non-apt-get RUN command",
			raw:  `RUN echo hello world`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw: `RUN echo hello world`,
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "echo",
											Args:    []string{"hello", "world"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "commented RUN command",
			raw: `# This is a comment
RUN echo hello world
# Another comment`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:   `RUN echo hello world`,
						Extra: "# This is a comment\n",
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "echo",
											Args:    []string{"hello", "world"},
										},
									},
								},
							},
						},
					},
					{
						Raw: "# Another comment",
					},
				},
			},
		},
		{
			name: "yum install command",
			raw:  `RUN yum install -y nginx`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN yum install -y nginx`,
						Converted: `RUN apk add --no-cache nginx`,
						Run: &RunDetails{
							Distro:   DistroFedora,
							Manager:  ManagerYum,
							Packages: []string{"nginx"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "yum",
											Args:    []string{"install", "-y", "nginx"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "nginx"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "dnf install command",
			raw:  `RUN dnf install -y nginx httpd php`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN dnf install -y nginx httpd php`,
						Converted: `RUN apk add --no-cache httpd nginx php`,
						Run: &RunDetails{
							Distro:   DistroFedora,
							Manager:  ManagerDnf,
							Packages: []string{"httpd", "nginx", "php"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "dnf",
											Args:    []string{"install", "-y", "nginx", "httpd", "php"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "httpd", "nginx", "php"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:     "mixed package manager commands",
			raw:      `RUN apt-get update && apt-get install -y nginx && yum install php`,
			expected: nil,
		},
		{
			name: "apk alpine to apk chainguard",
			raw:  `RUN apk update && apk add nginx`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN apk update && apk add nginx`,
						Converted: `RUN apk add --no-cache nginx`,
						Run: &RunDetails{
							Distro:   DistroAlpine,
							Manager:  ManagerApk,
							Packages: []string{"nginx"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apk",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command: "apk",
											Args:    []string{"add", "nginx"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "nginx"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "duplicated packages",
			raw:  `RUN apt-get install -y nginx nginx curl curl`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN apt-get install -y nginx nginx curl curl`,
						Converted: `RUN apk add --no-cache curl nginx`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"curl", "nginx"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apt-get",
											Args:    []string{"install", "-y", "nginx", "nginx", "curl", "curl"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "curl", "nginx"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple package manager commands in sequence",
			raw:  `RUN apt-get install -y nginx && apt-get install -y curl && apt-get install -y vim`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN apt-get install -y nginx && apt-get install -y curl && apt-get install -y vim`,
						Converted: `RUN apk add --no-cache curl nginx vim`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"curl", "nginx", "vim"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"install", "-y", "nginx"},
											Delimiter: "&&",
										},
										{
											Command:   "apt-get",
											Args:      []string{"install", "-y", "curl"},
											Delimiter: "&&",
										},
										{
											Command: "apt-get",
											Args:    []string{"install", "-y", "vim"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "curl", "nginx", "vim"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "mixed packages with duplicates",
			raw:  `RUN apt-get update && apt-get install -y nginx curl vim && apt-get install -y curl nginx`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN apt-get update && apt-get install -y nginx curl vim && apt-get install -y curl nginx`,
						Converted: `RUN apk add --no-cache curl nginx vim`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"curl", "nginx", "vim"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command:   "apt-get",
											Args:      []string{"install", "-y", "nginx", "curl", "vim"},
											Delimiter: "&&",
										},
										{
											Command: "apt-get",
											Args:    []string{"install", "-y", "curl", "nginx"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "curl", "nginx", "vim"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "package manager commands combined with non-pm commands",
			raw:  `RUN echo hello; apt-get update && apt-get install -y nginx curl vim && apt-get install -y curl nginx && echo goodbye`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw: `RUN echo hello; apt-get update && apt-get install -y nginx curl vim && apt-get install -y curl nginx && echo goodbye`,
						Converted: `RUN echo hello ; \
    apk add --no-cache curl nginx vim && \
    echo goodbye`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"curl", "nginx", "vim"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "echo",
											Args:      []string{"hello"},
											Delimiter: ";",
										},
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command:   "apt-get",
											Args:      []string{"install", "-y", "nginx", "curl", "vim"},
											Delimiter: "&&",
										},
										{
											Command:   "apt-get",
											Args:      []string{"install", "-y", "curl", "nginx"},
											Delimiter: "&&",
										},
										{
											Command: "echo",
											Args:    []string{"goodbye"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "echo",
											Args:      []string{"hello"},
											Delimiter: ";",
										},
										{
											Command:   "apk",
											Args:      []string{"add", "--no-cache", "curl", "nginx", "vim"},
											Delimiter: "&&",
										},
										{
											Command: "echo",
											Args:    []string{"goodbye"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple installs get combined",
			raw:  `RUN apt-get update && apt-get install -y nginx && apt-get install -y vim curl`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN apt-get update && apt-get install -y nginx && apt-get install -y vim curl`,
						Converted: `RUN apk add --no-cache curl nginx vim`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"curl", "nginx", "vim"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command:   "apt-get",
											Args:      []string{"install", "-y", "nginx"},
											Delimiter: "&&",
										},
										{
											Command: "apt-get",
											Args:    []string{"install", "-y", "vim", "curl"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "curl", "nginx", "vim"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "package mapping occurs properly - has mapping",
			raw:  `RUN apt-get update && apt-get install -y abc nginx`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN apt-get update && apt-get install -y abc nginx`,
						Converted: `RUN apk add --no-cache lmnop nginx xyz`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"abc", "nginx"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command: "apt-get",
											Args:    []string{"install", "-y", "abc", "nginx"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "lmnop", "nginx", "xyz"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "package mapping occurs properly - no mapping",
			raw:  `RUN yum install -y nginx abc`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN yum install -y nginx abc`,
						Converted: `RUN apk add --no-cache abc nginx`,
						Run: &RunDetails{
							Distro:   DistroFedora,
							Manager:  ManagerYum,
							Packages: []string{"abc", "nginx"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "yum",
											Args:    []string{"install", "-y", "nginx", "abc"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "apk",
											Args:    []string{"add", "--no-cache", "abc", "nginx"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "image with digest",
			raw:  `FROM python:3.9-slim@sha256:123456abcdef`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `FROM python:3.9-slim@sha256:123456abcdef`,
						Converted: `FROM cgr.dev/ORG/python:3.9`,
						Stage:     1,
						From: &FromDetails{
							Base:   "python",
							Tag:    "3.9-slim",
							Digest: "sha256:123456abcdef",
						},
					},
				},
			},
		},
		{
			name: "complex command with package manager and user management",
			raw:  `RUN apt-get update && apt-get install -y nginx && echo hello && ` + CommandUserAdd + ` myuser`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw: `RUN apt-get update && apt-get install -y nginx && echo hello && ` + CommandUserAdd + ` myuser`,
						Converted: `RUN apk add --no-cache nginx && \
    echo hello && \
    ` + CommandAddUser + ` myuser`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"nginx"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command:   "apt-get",
											Args:      []string{"install", "-y", "nginx"},
											Delimiter: "&&",
										},
										{
											Command:   "echo",
											Args:      []string{"hello"},
											Delimiter: "&&",
										},
										{
											Command: CommandUserAdd,
											Args:    []string{"myuser"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apk",
											Args:      []string{"add", "--no-cache", "nginx"},
											Delimiter: "&&",
										},
										{
											Command:   "echo",
											Args:      []string{"hello"},
											Delimiter: "&&",
										},
										{
											Command: CommandAddUser,
											Args:    []string{"myuser"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "complex command with package manager and user management plus shadow installed",
			raw:  `RUN apt-get update && apt-get install -y nginx shadow && echo hello && ` + CommandUserAdd + ` myuser`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw: `RUN apt-get update && apt-get install -y nginx shadow && echo hello && ` + CommandUserAdd + ` myuser`,
						Converted: `RUN apk add --no-cache nginx shadow && \
    echo hello && \
    ` + CommandUserAdd + ` myuser`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"nginx", "shadow"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command:   "apt-get",
											Args:      []string{"install", "-y", "nginx", "shadow"},
											Delimiter: "&&",
										},
										{
											Command:   "echo",
											Args:      []string{"hello"},
											Delimiter: "&&",
										},
										{
											Command: CommandUserAdd,
											Args:    []string{"myuser"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apk",
											Args:      []string{"add", "--no-cache", "nginx", "shadow"},
											Delimiter: "&&",
										},
										{
											Command:   "echo",
											Args:      []string{"hello"},
											Delimiter: "&&",
										},
										{
											Command: CommandUserAdd,
											Args:    []string{"myuser"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "basic_tar_extract_command",
			raw:  `RUN tar xf archive.tar`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN tar xf archive.tar`,
						Converted: `RUN tar -x -f archive.tar`,
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "tar",
											Args:    []string{"xf", "archive.tar"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "tar",
											Args:    []string{"-x", "-f", "archive.tar"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "tar_with_verbose_and_long_options",
			raw:  `RUN tar --extract --verbose --file=archive.tar`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN tar --extract --verbose --file=archive.tar`,
						Converted: `RUN tar -x -v -f archive.tar`,
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "tar",
											Args:    []string{"--extract", "--verbose", "--file=archive.tar"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "tar",
											Args:    []string{"-x", "-v", "-f", "archive.tar"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "complex_command_with_package_manager_and_tar",
			raw:  `RUN apt-get update && apt-get install -y wget && wget file.tar.gz && tar -xzf file.tar.gz -C /opt`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw: `RUN apt-get update && apt-get install -y wget && wget file.tar.gz && tar -xzf file.tar.gz -C /opt`,
						Converted: `RUN apk add --no-cache wget && \
    wget file.tar.gz && \
    tar -C /opt -xzf file.tar.gz`,
						Run: &RunDetails{
							Distro:   DistroDebian,
							Manager:  ManagerAptGet,
							Packages: []string{"wget"},
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apt-get",
											Args:      []string{"update"},
											Delimiter: "&&",
										},
										{
											Command:   "apt-get",
											Args:      []string{"install", "-y", "wget"},
											Delimiter: "&&",
										},
										{
											Command:   "wget",
											Args:      []string{"file.tar.gz"},
											Delimiter: "&&",
										},
										{
											Command: "tar",
											Args:    []string{"-xzf", "file.tar.gz", "-C", "/opt"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command:   "apk",
											Args:      []string{"add", "--no-cache", "wget"},
											Delimiter: "&&",
										},
										{
											Command:   "wget",
											Args:      []string{"file.tar.gz"},
											Delimiter: "&&",
										},
										{
											Command: "tar",
											Args:    []string{"-C", "/opt", "-xzf", "file.tar.gz"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "tar_with_unsupported_options",
			raw:  `RUN tar --extract --same-owner --numeric-owner --file archive.tar`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `RUN tar --extract --same-owner --numeric-owner --file archive.tar`,
						Converted: `RUN tar -x -f archive.tar`,
						Run: &RunDetails{
							Shell: &RunDetailsShell{
								Before: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "tar",
											Args:    []string{"--extract", "--same-owner", "--numeric-owner", "--file", "archive.tar"},
										},
									},
								},
								After: &ShellCommand{
									Parts: []*ShellPart{
										{
											Command: "tar",
											Args:    []string{"-x", "-f", "archive.tar"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "dynamic base image with ARG",
			raw: `ARG BASE_IMAGE=debian:bookworm-slim
FROM $BASE_IMAGE AS base`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `ARG BASE_IMAGE=debian:bookworm-slim`,
						Converted: `ARG BASE_IMAGE=cgr.dev/ORG/` + DefaultChainguardBase + `:latest`,
						Arg: &ArgDetails{
							Name:         "BASE_IMAGE",
							DefaultValue: `cgr.dev/ORG/` + DefaultChainguardBase + `:latest`,
							UsedAsBase:   true,
						},
					},
					{
						Raw:   `FROM $BASE_IMAGE AS base`,
						Stage: 1,
						From: &FromDetails{
							Base:        "$BASE_IMAGE",
							Alias:       "base",
							BaseDynamic: true,
						},
					},
				},
			},
		},
		{
			name: "dynamic base image with ARG, different syntax",
			raw: `ARG BASE_IMAGE=debian:bookworm-slim
FROM ${BASE_IMAGE} AS base`,
			expected: &Dockerfile{
				Lines: []*DockerfileLine{
					{
						Raw:       `ARG BASE_IMAGE=debian:bookworm-slim`,
						Converted: `ARG BASE_IMAGE=cgr.dev/ORG/` + DefaultChainguardBase + `:latest`,
						Arg: &ArgDetails{
							Name:         "BASE_IMAGE",
							DefaultValue: `cgr.dev/ORG/` + DefaultChainguardBase + `:latest`,
							UsedAsBase:   true,
						},
					},
					{
						Raw:   `FROM ${BASE_IMAGE} AS base`,
						Stage: 1,
						From: &FromDetails{
							Base:        "${BASE_IMAGE}",
							Alias:       "base",
							BaseDynamic: true,
						},
					},
				},
			},
		},
	}

	for _, tt := range convertTests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expected == nil {
				t.Skip("Test skipped")
				return
			}

			ctx := context.Background()
			parsed, err := ParseDockerfile(ctx, []byte(tt.raw))
			if err != nil {
				t.Fatalf("Failed to parse Dockerfile: %v", err)
			}

			converted, err := parsed.Convert(ctx, Options{
				ExtraMappings: MappingsConfig{
					Images: map[string]string{
						"debian": DefaultChainguardBase + ":latest",
					},
					Packages: PackageMap{
						DistroDebian: {
							"abc": []string{"xyz", "lmnop"},
						},
					},
				},
				Update:    false,
				NoBuiltIn: false,
			})
			if err != nil {
				t.Fatalf("Failed to convert Dockerfile: %v", err)
			}
			if diff := cmp.Diff(tt.expected, converted); diff != "" {
				t.Errorf("Dockerfile not as expected (-want, +got):\n%s\n", diff)
			}
		})
	}
}

// TestFullFileConversion checks that .before. Dockerfiles convert to .after. Dockerfiles
func TestFullFileConversion(t *testing.T) {
	// Find all .before.Dockerfile files in the testdata directory
	beforeFiles, err := filepath.Glob("../../testdata/*.before.Dockerfile")
	if err != nil {
		t.Fatalf("Failed to find test files: %v", err)
	}

	// Test each file
	for _, beforeFile := range beforeFiles {
		name := strings.Split(filepath.Base(beforeFile), ".")[0]
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			// Read the input file
			before, err := os.ReadFile(beforeFile)
			if err != nil {
				t.Fatalf("Failed to read input file: %v", err)
			}

			// Determine the expected output file
			afterFile := strings.Replace(beforeFile, ".before.", ".after.", 1)
			after, err := os.ReadFile(afterFile)
			if err != nil {
				t.Fatalf("Failed to read expected output file: %v", err)
			}

			// Parse and convert
			orig, err := ParseDockerfile(ctx, before)
			if err != nil {
				t.Fatalf("Failed to parse Dockerfile: %v", err)
			}
			converted, err := orig.Convert(ctx, Options{})
			if err != nil {
				t.Fatalf("Failed to convert Dockerfile: %v", err)
			}

			got := converted.String()
			want := string(after)

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("conversion not as expected (-want, +got):\n%s\n", diff)
			}
		})
	}
}

func TestDoubleConversionUserRoot(t *testing.T) {
	// Create a simple Dockerfile with a FROM and RUN instruction
	content := `FROM python:3.9
RUN apt-get update && apt-get install -y nano`

	ctx := context.Background()

	// Parse the Dockerfile
	dockerfile, err := ParseDockerfile(ctx, []byte(content))
	if err != nil {
		t.Fatalf("Failed to parse test Dockerfile: %v", err)
	}

	// Create options
	opts := Options{
		ExtraMappings: MappingsConfig{
			Packages: PackageMap{
				DistroDebian: {
					"nano": []string{"nano"},
				},
			},
		},
		Update:    false,
		NoBuiltIn: false,
	}

	// First conversion
	convertedOnce, err := dockerfile.Convert(ctx, opts)
	if err != nil {
		t.Fatalf("First conversion failed: %v", err)
	}

	// Get string result of first conversion
	firstResult := convertedOnce.String()

	// Now parse and convert the result again
	dockerfileTwice, err := ParseDockerfile(ctx, []byte(firstResult))
	if err != nil {
		t.Fatalf("Failed to parse the first conversion result: %v", err)
	}

	// Second conversion
	convertedTwice, err := dockerfileTwice.Convert(ctx, opts)
	if err != nil {
		t.Fatalf("Second conversion failed: %v", err)
	}

	// Get string result of second conversion
	secondResult := convertedTwice.String()

	// Count occurrences of "USER root" in both results
	userRootCount1 := strings.Count(firstResult, "USER root")
	userRootCount2 := strings.Count(secondResult, "USER root")

	if userRootCount1 != 1 {
		t.Errorf("Expected exactly 1 USER root directive in first conversion, got %d", userRootCount1)
	}

	if userRootCount2 != 1 {
		t.Errorf("Expected exactly 1 USER root directive in second conversion, got %d", userRootCount2)
	}

	// Also ensure the results are identical (idempotent)
	if firstResult != secondResult {
		t.Errorf("Converting twice produced different results:\nFirst:\n%s\nSecond:\n%s", firstResult, secondResult)
	}
}

// TestNoBuiltInOption tests that the NoBuiltIn option correctly skips the default mappings
func TestNoBuiltInOption(t *testing.T) {
	// Create a simple Dockerfile
	content := `FROM debian:latest
RUN apt-get update && apt-get install -y nano`

	ctx := context.Background()

	// Parse the Dockerfile
	dockerfile, err := ParseDockerfile(ctx, []byte(content))
	if err != nil {
		t.Fatalf("Failed to parse test Dockerfile: %v", err)
	}

	// Case 1: With NoBuiltIn=false (default behavior)
	opts1 := Options{
		NoBuiltIn: false,
		// Override the default mappings for testing
		ExtraMappings: MappingsConfig{
			Images: map[string]string{
				"debian": "cgr.dev/test/debian:latest",
			},
		},
	}

	// Case 2: With NoBuiltIn=true
	opts2 := Options{
		NoBuiltIn: true,
		// Provide the same mappings as ExtraMappings
		ExtraMappings: MappingsConfig{
			Images: map[string]string{
				"debian": "cgr.dev/test/debian:latest",
			},
		},
	}

	// Case 3: With NoBuiltIn=true and no ExtraMappings
	opts3 := Options{
		NoBuiltIn: true,
		// No ExtraMappings
	}

	// Convert with each option
	converted1, err := dockerfile.Convert(ctx, opts1)
	if err != nil {
		t.Fatalf("Convert with NoBuiltIn=false failed: %v", err)
	}

	converted2, err := dockerfile.Convert(ctx, opts2)
	if err != nil {
		t.Fatalf("Convert with NoBuiltIn=true failed: %v", err)
	}

	converted3, err := dockerfile.Convert(ctx, opts3)
	if err != nil {
		t.Fatalf("Convert with NoBuiltIn=true and no ExtraMappings failed: %v", err)
	}

	// Get string results
	result1 := converted1.String()
	result2 := converted2.String()
	result3 := converted3.String()

	// Check that result1 and result2 are the same (since they have the same mappings)
	if !strings.Contains(result1, "cgr.dev/test/debian:latest") {
		t.Errorf("Expected result1 to contain 'cgr.dev/test/debian:latest', got: %s", result1)
	}

	if !strings.Contains(result2, "cgr.dev/test/debian:latest") {
		t.Errorf("Expected result2 to contain 'cgr.dev/test/debian:latest', got: %s", result2)
	}

	// Check that result3 still has conversion for the FROM line, just using default registry/org
	if !strings.Contains(result3, "cgr.dev/ORG/debian:latest-dev") {
		t.Errorf("Expected result3 to contain 'cgr.dev/ORG/debian:latest-dev', got: %s", result3)
	}

	// Verify that the RUN command is still converted in all cases
	for i, result := range []string{result1, result2, result3} {
		if !strings.Contains(result, "RUN apk add --no-cache nano") {
			t.Errorf("Expected result%d to convert apt-get to apk add, got: %s", i+1, result)
		}
	}
}
