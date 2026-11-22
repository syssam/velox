-- script: main

-- review: zero-default: adds column users.nickname as NOT NULL with default ''; existing rows get '': give the field a Default or make it Nillable if that is not their value
ALTER TABLE "users" ADD COLUMN "nickname" text NOT NULL DEFAULT '';
