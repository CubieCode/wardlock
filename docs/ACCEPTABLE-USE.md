# Acceptable use policy

> **Draft for legal review.** This policy has not yet been reviewed by a lawyer. It is the maintainer's working draft and may change before it is final.

This policy covers everyone with an account on a Wardlock server (an "owner") and everything they publish through it. "The operator" is whoever runs the server. Creating an account, by completing setup or opening an invitation and continuing, means you agree to it.

**Do not publish, link to or distribute:**

- anything illegal where the server runs or where you are;
- sexual or obscene content of any kind;
- anything that sexualises, exploits or endangers minors;
- malware, phishing pages, scams, or pages that impersonate another person or organisation;
- spam, or pages made to promote spam;
- content that infringes someone else's copyright, trademark, privacy or other rights.

Content involving the sexual exploitation of children is never tolerated. The operator removes it, suspends the account and reports it to the authorities.

<details>
<summary>What each rule covers</summary>

- **Illegal content** includes anything unlawful to publish or possess in New Zealand (where Wardlock is made) or in the country where the operator runs the server, such as objectionable publications, threats, harassment, incitement to violence and the sale of illegal goods or services.
- **Sexual or obscene content** includes pornography, sexually explicit images, video or text, and obscene material, whether or not it is lawful elsewhere and whether or not a site's access policy limits who can see it.
- **Minors.** Nothing that depicts, describes or encourages the sexual abuse or exploitation of anyone under 18, including drawn, generated or altered images, and nothing that helps anyone contact or groom a child. There are no exceptions and no warnings.
- **Malware, phishing and impersonation** includes pages that collect passwords or payment details under false pretences, downloads that harm or take control of a visitor's device, cryptocurrency or investment scams, and pages made to look like another person, company, bank or government agency.
- **Spam** includes pages sent in unsolicited bulk messages and pages made only to promote such messages or to manipulate search results.
- **Others' rights** includes copying someone else's work, brand or personal information without permission, and publishing private information about a person (doxxing).

Access control does not change these rules: content behind a password, share link or sign-in must follow them as much as public content.

</details>

## Screening, removal and suspension

The operator may check what you publish, and may act without notice.

- **Automated screening.** The operator may screen content automatically. When screening is turned on, images and the text of pages may be sent to OpenAI's moderation service, which checks them and returns categories such as sexual content or violence; flagged files may be withheld from your sites while the operator reviews them. The operator can exempt individual owners from screening.
- **Removal and takedown.** The operator may remove content, take any site offline, or delete it, at any time and without notice, if they believe it breaks this policy or the law.
- **Suspension.** The operator may suspend an account without notice. A suspended owner cannot sign in or use API tokens, and their sites stop serving.
- **Evidence and reporting.** Content that may be illegal is kept, not deleted, and may be passed to the police or other authorities.

<details>
<summary>How screening and enforcement work</summary>

- Screening is off unless the operator turns it on. When it is on, each unique image and page is checked once, after a deploy is activated; videos and other file types are not sent. OpenAI's handling of the content it receives is governed by OpenAI's own terms. Screening never blocks a deploy, and a withheld file does not stop the rest of the site from working. Automated screening on a Wardlock server is planned work (roadmap item A2.15); this section describes what the operator may do once it is available.
- A site taken offline answers a neutral "This site is unavailable" page on every address it uses, including custom domains. Its deploys, files and access log are kept until the operator decides what to do with them.
- The operator may contact you about a report and ask you to change or remove content, but does not have to before acting. The operator may restore a site or reinstate an account if a report turns out to be unfounded.
- The operator decides how to apply this policy to their own server and may add rules of their own.

</details>

## Reporting abuse

To report a site on a Wardlock server, use the **Report this site** link at the foot of its sign-in page, its share-link page or its unavailable page. The link opens a short form on the server's dashboard address (`/report?site=<host>`) that emails the operator. You can leave a contact address if you want a reply; your IP address is not sent.

If you believe a site shows a child being sexually abused, report it to the authorities as well. In New Zealand, report it to the Department of Internal Affairs ([dia.govt.nz](https://www.dia.govt.nz)) or the NZ Police ([police.govt.nz](https://www.police.govt.nz); 111 in an emergency). Do not download, copy or share the material.

<details>
<summary>When the report link is missing</summary>

The link appears only when the operator has set an abuse address. If a server shows no report link, contact the operator through the address they publish for their server. To report a security problem in Wardlock itself rather than in content, follow the repository's security policy (SECURITY.md) instead.

</details>
