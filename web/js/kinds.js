// What an app's kind looks like: language logo, runtime label, base image, status.
import { h } from "/assets/js/dom.js";

// Full literal paths: the server rewrites them to hashed URLs when it serves the panel.
const LOGOS = {
  jar: "/assets/vendor/devicon/java-original.svg",
  native: "/assets/vendor/devicon/linux-original.svg",
  postgres: "/assets/vendor/devicon/postgresql-original.svg",
};

export function kindLogo(kind) {
  return h("img", { src: kind === "jar" ? LOGOS.jar : LOGOS.native, alt: "" });
}

export const pgLogo = () => h("img", { src: LOGOS.postgres, alt: "" });

export function runtimeLabel(app) {
  if (app.kind === "jar") return `Java ${app.java_version ?? ""}`.trim();
  return app.kind === "static" ? "Binary statis" : "Binary glibc";
}

export function runtimeImage(app) {
  if (app.kind === "jar") return `eclipse-temurin:${app.java_version}-jre`;
  return app.kind === "static" ? "gcr.io/distroless/static-debian12" : "ubuntu:26.04";
}

/** [css class, label] of an app status. */
export function statusOf(status) {
  switch (status) {
    case "running":
      return ["ok", "Berjalan"];
    case "deploying":
      return ["warn", "Sedang deploy"];
    case "failed":
      return ["err", "Gagal"];
    default:
      return ["", "Berhenti"];
  }
}

export function deployStatusLabel(d, activeId) {
  if (d.id === activeId || d.status === "active") return ["ok", "aktif"];
  switch (d.status) {
    case "failed":
      return ["err", "gagal"];
    case "pending":
    case "running":
      return ["warn", "berjalan"];
    default:
      return ["", ""];
  }
}

/** The runtime ids the admin can switch (Server page), with a label and image. */
export const RUNTIMES = [
  { id: "java8", label: "Java 8", image: "eclipse-temurin:8-jre", kind: "jar" },
  { id: "java11", label: "Java 11", image: "eclipse-temurin:11-jre", kind: "jar" },
  { id: "java17", label: "Java 17", image: "eclipse-temurin:17-jre", kind: "jar" },
  { id: "java21", label: "Java 21", image: "eclipse-temurin:21-jre", kind: "jar" },
  { id: "java25", label: "Java 25", image: "eclipse-temurin:25-jre", kind: "jar" },
  { id: "static", label: "Binary statis", image: "gcr.io/distroless/static-debian12", kind: "static" },
  { id: "glibc", label: "Binary glibc", image: "ubuntu:26.04", kind: "glibc" },
];

export const JAVA_VERSIONS = [8, 11, 17, 21, 25];
export const LTS_NOTE = { 8: "LTS lama", 11: "LTS", 17: "LTS", 21: "LTS, disarankan", 25: "LTS terbaru" };
