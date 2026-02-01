/**
 * Comment type for issue comments.
 */

import type { ISODateString } from './common';

/**
 * Comment on an issue.
 * Maps to Go types.Comment.
 */
export interface Comment {
  id: number;
  issue_id: string;
  author: string;
  text: string;
  created_at: ISODateString;
}
