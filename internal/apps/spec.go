package apps

import (
	"strconv"
	"strings"

	"runova/internal/artifact"
	"runova/internal/docker"
	"runova/internal/store"
)

const (
	staticImage = "gcr.io/distroless/static-debian12"
	glibcImage  = "ubuntu:26.04"
	appMount    = "/app"
	dataMount   = "/data"
	// exec: Docker mounts tmpfs noexec by default, which breaks JVM apps that
	// unpack native libraries into /tmp (Netty, sqlite-jdbc, zstd-jni). /data is
	// a writable, executable bind mount anyway, so noexec protected nothing.
	// nosuid and nodev stay (Docker's defaults), as does no-new-privileges.
	tmpOptions = "rw,exec,size=64m,mode=1777"
)

func networkName(userID int64) string { return "runova-u" + strconv.FormatInt(userID, 10) }

func containerName(owner store.User, app store.App, d store.Deployment) string {
	return "runova-" + owner.Username + "-" + app.Name + "-" + strconv.FormatInt(d.ID, 10)
}

// imageFor returns the base image for the deployment.
func imageFor(app store.App, d store.Deployment) string {
	switch app.Kind {
	case artifact.KindJar:
		v := 0
		if d.JavaVersion != nil {
			v = *d.JavaVersion
		}
		return "eclipse-temurin:" + strconv.Itoa(v) + "-jre"
	case artifact.KindStatic:
		return staticImage
	default:
		return glibcImage
	}
}

// ContainerSpec builds the container for deployment d. It is pure: env holds
// the user's variables in plain text and dbEnv the database variables
// ("NAME=value"). Later sources win, and Runova's own variables win over all.
func (s *Service) ContainerSpec(app store.App, user store.User, d store.Deployment, env []store.EnvEntry, dbEnv []string) docker.ContainerSpec {
	file := appMount + "/" + d.FileName
	cmd := []string{file}
	if app.Kind == artifact.KindJar {
		cmd = append([]string{"java"}, strings.Fields(d.JVMOpts)...)
		cmd = append(cmd, "-jar", file)
	}

	vars := make([]string, 0, len(env)+len(dbEnv)+3)
	index := map[string]int{}
	set := func(name, value string) {
		kv := name + "=" + value
		if i, ok := index[name]; ok {
			vars[i] = kv
			return
		}
		index[name] = len(vars)
		vars = append(vars, kv)
	}
	for _, e := range env {
		set(e.Name, e.Value)
	}
	for _, kv := range dbEnv {
		name, value, _ := strings.Cut(kv, "=")
		set(name, value)
	}
	set("PORT", strconv.Itoa(d.InternalPort))
	if app.Kind == artifact.KindJar {
		// Spring Boot binds server.port from SERVER_PORT and outranks the
		// application files with it, so the app listens where Runova checks.
		set("SERVER_PORT", strconv.Itoa(d.InternalPort))
	}
	set("RUNOVA_DATA_DIR", dataMount)
	set("HOME", "/tmp")

	uid := strconv.Itoa(containerUID(user))
	return docker.ContainerSpec{
		Image: imageFor(app, d),
		Cmd:   cmd,
		Env:   vars,
		User:  uid + ":" + uid,
		Labels: map[string]string{
			"runova.user":       user.Username,
			"runova.app":        strconv.FormatInt(app.ID, 10),
			"runova.deployment": strconv.FormatInt(d.ID, 10),
		},
		WorkingDir:   dataMount,
		Network:      networkName(user.ID),
		InternalPort: d.InternalPort,
		HostPort:     d.HostPort,
		Binds: []string{
			d.ArtifactPath + ":" + file + ":ro",
			s.appDataDir(user.Username, app.Name) + ":" + dataMount,
		},
		Tmpfs:       map[string]string{"/tmp": tmpOptions},
		MemoryBytes: int64(app.MemoryMB) << 20,
		NanoCPUs:    int64(app.CPUMilli) * 1_000_000,
		ReadOnly:    true,
		ExtraHosts:  []string{"host.docker.internal:host-gateway"},
	}
}
