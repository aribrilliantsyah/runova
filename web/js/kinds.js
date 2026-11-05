// What an app's kind looks like: language logo, runtime label, base image, status.
import { h, icon } from "/assets/js/dom.js";

// Full literal paths: the server rewrites them to hashed URLs when it serves the panel.
const LOGOS = {
  java: "/assets/vendor/devicon/java-original.svg",
  kotlin: "/assets/vendor/devicon/kotlin-original.svg",
  scala: "/assets/vendor/devicon/scala-original.svg",
  groovy: "/assets/vendor/devicon/groovy-original.svg",
  clojure: "/assets/vendor/devicon/clojure-original.svg",
  go: "/assets/vendor/devicon/go-original.svg",
  rust: "/assets/vendor/devicon/rust-original.svg",
  zig: "/assets/vendor/devicon/zig-original.svg",
  cpp: "/assets/vendor/devicon/cplusplus-original.svg",
  c: "/assets/vendor/devicon/c-original.svg",
  dotnet: "/assets/vendor/devicon/dotnetcore-original.svg",
  postgres: "/assets/vendor/devicon/postgresql-original.svg",
};

/** Display names of the languages the server reports (artifact.Report.Language). */
const LANGUAGE_NAMES = {
  java: "Java",
  kotlin: "Kotlin",
  scala: "Scala",
  groovy: "Groovy",
  clojure: "Clojure",
  go: "Go",
  rust: "Rust",
  zig: "Zig",
  cpp: "C++",
  c: "C",
  dotnet: ".NET",
};

/** Logos that are black on transparent and need inverting in the dark theme. */
const MONOCHROME = new Set(["rust"]);

/**
 * kindLogo(kind, language) → the app's language logo. A JAR without a known language is Java;
 * a binary without one gets a neutral icon (muted, so it does not pass for a broken logo).
 */
export function kindLogo(kind, language) {
  const lang = language || (kind === "jar" ? "java" : "");
  if (LOGOS[lang] && LANGUAGE_NAMES[lang]) {
    return h("img", { src: LOGOS[lang], alt: "", class: MONOCHROME.has(lang) ? "inv" : "" });
  }
  return icon(kind === "glibc" ? "box" : "binary", "neutral");
}

export const pgLogo = () => h("img", { src: LOGOS.postgres, alt: "" });

export function runtimeLabel(app) {
  if (app.kind === "jar") return `Java ${app.java_version ?? ""}`.trim();
  return app.kind === "static" ? "Binary statis" : "Binary glibc";
}

/** runtimeSub(app, language) → the card subtitle: "Go · Binary statis", "Kotlin · Java 21", "Java 21". */
export function runtimeSub(app, language) {
  const runtime = runtimeLabel(app);
  const name = LANGUAGE_NAMES[language];
  return name && !(app.kind === "jar" && language === "java") ? `${name} · ${runtime}` : runtime;
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
    case "starting":
      return ["warn", "Memulai…"];
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
