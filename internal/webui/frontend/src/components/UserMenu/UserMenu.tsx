import { useState, useCallback, useRef, useEffect } from "react";

import { useAuth } from "@/contexts/AuthContext";
import { AUTH_MODE_OIDC } from "@/api/appConfig";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./UserMenu.module.css";

export function UserMenu(): JSX.Element | null {
  const { mode, user, isAuthenticated, signOut } = useAuth();

  const [isOpen, setIsOpen] = useState(false);
  const [signingOut, setSigningOut] = useState(false);
  const [imgError, setImgError] = useState(false);

  const wrapperRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  // Reset image error state if user image changes
  useEffect(() => {
    setImgError(false);
  }, [user?.image]);

  // Click outside to close
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (
        wrapperRef.current &&
        !wrapperRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [isOpen]);

  // Escape to close
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsOpen(false);
        triggerRef.current?.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isOpen]);

  const handleSignOut = useCallback(async () => {
    setSigningOut(true);
    try {
      await signOut();
    } catch (err) {
      console.warn("Sign out failed:", err);
    } finally {
      setSigningOut(false);
      setIsOpen(false);
    }
  }, [signOut]);

  // Self-gate: only render when mode=external and authenticated
  if (mode !== AUTH_MODE_OIDC || !isAuthenticated || !user) {
    return null;
  }

  const displayName = user.name || user.email || "User";
  const initial = displayName.charAt(0).toUpperCase();
  const avatarColor = getAvatarColor(displayName);
  const avatarTextColor = shouldUseWhiteText(avatarColor) ? "#fff" : "#1f2937";
  const showImg = user.image && !imgError;

  return (
    <div ref={wrapperRef} className={styles.wrapper}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        onClick={() => setIsOpen((prev) => !prev)}
        aria-expanded={isOpen}
        aria-haspopup="menu"
        aria-label={`User menu for ${displayName}`}
        data-testid="user-menu-trigger"
      >
        {showImg ? (
          <img
            src={user.image!}
            alt=""
            className={styles.avatarImg}
            onError={() => setImgError(true)}
          />
        ) : (
          <span
            className={styles.avatarInitial}
            style={{ backgroundColor: avatarColor, color: avatarTextColor }}
          >
            {initial}
          </span>
        )}
        <span className={styles.userName}>{displayName}</span>
        <svg
          className={`${styles.chevron} ${isOpen ? styles.chevronOpen : ""}`}
          width="12"
          height="12"
          viewBox="0 0 12 12"
          fill="none"
          aria-hidden="true"
        >
          <path
            d="M3 4.5L6 7.5L9 4.5"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>

      {isOpen && (
        <div
          className={styles.dropdown}
          role="menu"
          data-testid="user-menu-dropdown"
        >
          <div className={styles.userInfo}>
            <div className={styles.userInfoName}>{displayName}</div>
            {user.email && (
              <div className={styles.userInfoEmail}>{user.email}</div>
            )}
          </div>
          <hr className={styles.separator} />
          <button
            type="button"
            className={styles.menuItem}
            role="menuitem"
            onClick={handleSignOut}
            disabled={signingOut}
            data-testid="user-menu-sign-out"
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 16 16"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M6 14H3.333A1.333 1.333 0 012 12.667V3.333A1.333 1.333 0 013.333 2H6M10.667 11.333L14 8l-3.333-3.333M14 8H6"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            {signingOut ? "Signing out..." : "Sign Out"}
          </button>
        </div>
      )}
    </div>
  );
}
