![Logo](http://svg.wiersma.co.za/glasslabs/module?title=CALENDAR&tag=a%20simple%20calendar%20module)

Calendar is a simple calendar module for [looking glass](http://github.com/glasslabs/looking-glass)

## Usage

Clone the calendar into a path under your modules path and add the module path
to under modules in your configuration.

```yaml
modules:
 - name: simple-calendar
   url:  https://github.com/glasslabs/calendar/releases/download/v1.0.0/calendar.wasm
   position: top:right
   config:
     timezone: Africa/Johannesburg
     maxDays: 5
     maxEvents: 20
     calendars:
       - url: https://www.calendarlabs.com/ical-calendar/ics/68/South_Africa_Holidays.ics
         maxEvents: 10
```

## Configuration

### Timezone (timezone)

*Default: UTC*

The timezone name according to [IANA Time Zone databse](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones).

### Max Days (maxDays)

*Default: 5*

The maximum number of days, including today, to display events for.

### Max Events (maxEvents)

*Default: 30*

The maximum number of events to display at any one time.

### Calendar URL (calendar.[].url)

*Required*

The url of the calendar in ICS format.

### Calendar Max Events (calendar.[].maxEvents)

*Optional*

The maximum number of events to display for this calendar at any one time.
