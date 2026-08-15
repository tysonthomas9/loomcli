export const SKILL_MD = "SKILL.md";
export const SKILL_NAME_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
export const ROLE_NAME_RE = /^[a-z0-9](?:[a-z0-9._-]{0,98}[a-z0-9])?$/;

const RESERVED_SKILL_NAMES = new Set(["anthropic", "claude"]);
const WINDOWS_DEVICE_NAMES = new Set([
  "con",
  "prn",
  "aux",
  "nul",
  ...Array.from({ length: 9 }, (_, index) => `com${index + 1}`),
  ...Array.from({ length: 9 }, (_, index) => `lpt${index + 1}`),
]);

export function validateSkillName(name: string): string | null {
  if (!name.trim()) return "Skill name is required";
  if (name.length > 64) return "Skill name must be at most 64 characters";
  if (!SKILL_NAME_RE.test(name)) {
    return "Use lowercase letters, digits, and internal hyphens only";
  }
  if (RESERVED_SKILL_NAMES.has(name)) return `Skill name “${name}” is reserved`;
  if (WINDOWS_DEVICE_NAMES.has(name)) {
    return `Skill name “${name}” is a reserved device name`;
  }
  return null;
}

export function validateRoleName(name: string): string | null {
  if (!ROLE_NAME_RE.test(name)) {
    return "Role name must be 1-100 lowercase characters with internal dots, underscores, or hyphens";
  }
  return null;
}

export function validateSkillDescription(description: string): string | null {
  if (!description.trim()) return "Description is required";
  if (new TextEncoder().encode(description).length > 1024) {
    return "Description must be at most 1024 bytes";
  }
  if (/[<>]/.test(description)) {
    return "Description must not contain angle brackets";
  }
  return null;
}

export function validateSkillFilePath(path: string): string | null {
  if (!path) return "File path is required";
  if (new TextEncoder().encode(path).length > 256) {
    return "File path must be at most 256 bytes";
  }
  if (/\p{Cc}/u.test(path))
    return "File path must not contain control characters";
  if (path.includes("\\")) return "File path must not contain backslashes";
  if (path.startsWith("/")) return "File path must be relative";
  if (path.startsWith("~")) return "File path must not start with ~";
  if (path.includes(":")) return "File path must not contain a colon";
  const segments = path.split("/");
  for (const [index, segment] of segments.entries()) {
    if (!segment) return "File path must not contain empty segments";
    if (segment === "." || segment === "..") {
      return `File path must not contain ${segment} segments`;
    }
    if (new TextEncoder().encode(segment).length > 255) {
      return "File path contains a segment longer than 255 bytes";
    }
    const folded = segment.normalize("NFC").toLocaleLowerCase("en-US");
    const device = folded.split(".")[0] ?? folded;
    if (WINDOWS_DEVICE_NAMES.has(device)) {
      return `File path uses reserved device name “${segment}”`;
    }
    if (index === 0 && folded === SKILL_MD.toLowerCase()) {
      return `${SKILL_MD} is reserved for the skill body`;
    }
  }
  return null;
}

export function parseSkillPath(
  path: string,
): { skill: string; file: string } | null {
  const clean = path.replace(/^\/+|\/+$/g, "");
  const slash = clean.indexOf("/");
  if (slash <= 0) return null;
  const skill = clean.slice(0, slash);
  const file = clean.slice(slash + 1);
  if (validateSkillName(skill)) return null;
  if (file !== SKILL_MD && validateSkillFilePath(file)) return null;
  return { skill, file };
}

export function skillFolderPath(skill: string): string {
  return skill;
}

export function skillPathKey(path: string): string {
  return path.normalize("NFC").toLocaleLowerCase("en-US");
}
