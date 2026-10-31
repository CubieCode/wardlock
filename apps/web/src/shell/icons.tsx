// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Line icons for the rail, navigator and jump-to. All decorative: the
// components that show them hide them and name the control in words.
import type { ReactNode } from 'react';

function Icon({ children }: { children: ReactNode }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  );
}

export const overviewIcon = (
  <Icon>
    <rect x="3" y="3" width="7" height="7" rx="2" />
    <rect x="14" y="3" width="7" height="7" rx="2" />
    <rect x="3" y="14" width="7" height="7" rx="2" />
    <rect x="14" y="14" width="7" height="7" rx="2" />
  </Icon>
);

/** A hub with sites on wires. */
export const sitesIcon = (
  <Icon>
    <circle cx="12" cy="12" r="3" />
    <circle cx="4.5" cy="6" r="2" />
    <circle cx="19.5" cy="6" r="2" />
    <circle cx="19.5" cy="18" r="2" />
    <path d="M6.3 7l3.5 3M17.7 7l-3.5 3M17.7 17l-3.5-3" />
  </Icon>
);

/** A tag, for groups and tags. */
export const organiseIcon = (
  <Icon>
    <path d="M3.5 12.5V4.5a1 1 0 0 1 1-1h8l8 8-9 9z" />
    <circle cx="8.5" cy="8.5" r="1.5" />
  </Icon>
);

export const passkeysIcon = (
  <Icon>
    <circle cx="9" cy="8" r="4" />
    <path d="M3 20c0-3.3 2.7-6 6-6h1M17 12a2.5 2.5 0 1 1 0 5 2.5 2.5 0 0 1 0-5zM17 17v4M17 19.5h2" />
  </Icon>
);

export const tokensIcon = (
  <Icon>
    <circle cx="8" cy="16" r="4" />
    <path d="M11 13l9-9M16 8l3 3M13 11l2 2" />
  </Icon>
);

export const notificationsIcon = (
  <Icon>
    <path d="M6 16.5V11a6 6 0 0 1 12 0v5.5l1.5 2h-15z" />
    <path d="M10 21a2.2 2.2 0 0 0 4 0" />
  </Icon>
);

export const webhooksIcon = (
  <Icon>
    <circle cx="6" cy="17" r="2.5" />
    <circle cx="18" cy="17" r="2.5" />
    <circle cx="12" cy="6" r="2.5" />
    <path d="M10.8 8.2 7.2 14.8M8.5 17h7M13.2 8.2l3.6 6.6" />
  </Icon>
);

export const accountIcon = (
  <Icon>
    <circle cx="12" cy="8" r="4" />
    <path d="M4 21c0-4.4 3.6-8 8-8s8 3.6 8 8" />
  </Icon>
);

export const searchIcon = (
  <Icon>
    <circle cx="11" cy="11" r="7" />
    <path d="M20 20l-4-4" />
  </Icon>
);

export const listIcon = (
  <Icon>
    <path d="M4 6h16M4 12h16M4 18h16" />
  </Icon>
);

export const pinIcon = (
  <Icon>
    <path d="M12 3l2.6 5.3 5.9.9-4.3 4.1 1 5.8L12 16.4 6.8 19.1l1-5.8-4.3-4.1 5.9-.9z" />
  </Icon>
);

export const clockIcon = (
  <Icon>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M12 7.5V12l3 2" />
  </Icon>
);

export const globeIcon = (
  <Icon>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M3.5 12h17M12 3.5c2.5 2.6 3.5 5.4 3.5 8.5s-1 5.9-3.5 8.5c-2.5-2.6-3.5-5.4-3.5-8.5s1-5.9 3.5-8.5z" />
  </Icon>
);

export const quietIcon = (
  <Icon>
    <path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z" />
  </Icon>
);

export const untaggedIcon = (
  <Icon>
    <path d="M3.5 12.5V4.5a1 1 0 0 1 1-1h8l8 8-9 9z" />
    <path d="M4 20L20 4" />
  </Icon>
);

export const downIcon = (
  <Icon>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M6 18L18 6" />
  </Icon>
);

/** An arrow for jump-to's pages. */
export const pageIcon = (
  <Icon>
    <path d="M9 6l6 6-6 6" />
  </Icon>
);

/** Opens the navigator drawer on small screens. */
export const filterIcon = (
  <Icon>
    <path d="M4 6h16M7 12h10M10 18h4" />
  </Icon>
);
