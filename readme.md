# feeder 📫 

Aggregates news feed updates and sends them to your email inbox.

- Supports Atom and RSS feeds.
- Supports subscribing to feed directly, or scanning for a `link` tag at a given URL.
- Uses Golang [html/template](https://golang.org/pkg/html/template/#pkg-overview) to customize the email body.
- Update timestamps persisted to YAML file.

## Usage

- Install via `go install github.com/fgeller/feeder` or download a [release](https://github.com/fgeller/feeder/releases).
- Create a [config file](https://github.com/fgeller/feeder#example-config), customizing email settings and file paths.
- Add subscribed feeds either by:
  - maintaing the [feeds config file](https://github.com/fgeller/feeder#example-feeds-config) manually, or
  - using feeder via `feeder -config cfg.yml -subscribe https://example.com/blog/`
- Run via `feeder -config cfg.yml` manually, or set up recurring execution, e.g. via `crontab -e`

## Configuration

- `feeds-file` is the list of feeds you are subscribed to.

- `timestamp-file` is required to persist what updates have been seen.

- `email-template-file` is an optional Golang [html/template](https://golang.org/pkg/html/template/#pkg-overview) to format the sent email.

- `email` contains the configuration for sending emails. The `from` address will
  also be the `to` address and the `smtp` object allows for standard smtp host
  and auth configuration.

- `feeds` is an array of objects with `name` and `url` string fields. Optionally you can set a bool `disabled` to temporarily pause a subscription.

### Example Config

```yaml
feeds-file: '/home/fgeller/.config/feeder/feeds.yml'
timestamp-file: '/home/fgeller/.config/feeder/timestamps.yml'
email-template-file: '/home/fgeller/.config/feeder/email.tmpl'
email:
  from: example@gmail.com
  smtp:
    host: smtp.gmail.com
    port: 587
    user: example@gmail.com
    pass: passwort
```

### Example Feeds Config

```yaml
- name: 'irreal'
  url: https://irreal.org/blog/?feed=rss2
- name: The Go Blog
  url: https://blog.golang.org/feed.atom
```

## Alternatives

- [blogtrottr](https://blogtrottr.com)
- [mailbrew](https://mailbrew.com/)
