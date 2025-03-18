package dfc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

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
						Converted: `RUN apk add -U nginx`,
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
											Args:    []string{"add", "-U", "nginx"},
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
						Converted: `RUN apk add -U curl nginx vim`,
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
											Args:    []string{"add", "-U", "curl", "nginx", "vim"},
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
						Converted: `RUN apk add -U nginx`,
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
											Args:    []string{"add", "-U", "nginx"},
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
						Converted: `RUN apk add -U nginx`,
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
											Args:    []string{"add", "-U", "nginx"},
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
						Converted: `RUN apk add -U httpd nginx php`,
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
											Args:    []string{"add", "-U", "httpd", "nginx", "php"},
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
						Converted: `RUN apk add -U nginx`,
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
											Args:    []string{"add", "-U", "nginx"},
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
						Converted: `RUN apk add -U curl nginx`,
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
											Args:    []string{"add", "-U", "curl", "nginx"},
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
						Converted: `RUN apk add -U curl nginx vim`,
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
											Args:    []string{"add", "-U", "curl", "nginx", "vim"},
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
						Converted: `RUN apk add -U curl nginx vim`,
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
											Args:    []string{"add", "-U", "curl", "nginx", "vim"},
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
    apk add -U curl nginx vim && \
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
											Args:      []string{"add", "-U", "curl", "nginx", "vim"},
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
						Converted: `RUN apk add -U curl nginx vim`,
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
											Args:    []string{"add", "-U", "curl", "nginx", "vim"},
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
						Converted: `RUN apk add -U lmnop nginx xyz`,
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
											Args:    []string{"add", "-U", "lmnop", "nginx", "xyz"},
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
						Converted: `RUN apk add -U abc nginx`,
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
											Args:    []string{"add", "-U", "abc", "nginx"},
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
						Converted: `FROM cgr.dev/ORG/python:3.9-dev`,
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
				PackageMap: PackageMap{
					DistroDebian: {
						"abc": []string{"xyz", "lmnop"},
					},
				},
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

	var mappingsBytes []byte
	mappingsBytes, err = os.ReadFile("../../packages.yaml")
	if err != nil {
		t.Fatalf("Failed to read mappings file: %v", err)
	}

	var packageMap PackageMap
	if err := yaml.Unmarshal(mappingsBytes, &packageMap); err != nil {
		t.Fatalf("Failed to unmarshal package mappings: %v", err)
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
			converted, err := orig.Convert(ctx, Options{
				PackageMap: packageMap,
			})
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
		PackageMap: PackageMap{
			DistroDebian: {
				"nano": []string{"nano"},
			},
		},
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
